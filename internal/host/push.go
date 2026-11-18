// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// PushRequest builds the images of a checkout and pushes them to the registry on the developer machine.
type PushRequest struct {
	Source    string   `json:"source,omitempty" jsonschema:"app-bricks-py checkout to build from; default the current directory when it is one, else the source set in ~/.config/arduino-test-buddy/config.yaml; whether it is a worktree is the caller's choice"`
	Tag       string   `json:"tag,omitempty" jsonschema:"image tag; default the branch name, or bt-<worktree folder> on a detached HEAD"`
	Targets   []string `json:"targets,omitempty" jsonschema:"bake targets to build and push; default every container, which also drops the build cache and the tags no push used for 3 days"`
	SkipWheel bool     `json:"skip_wheel,omitempty" jsonschema:"reuse dist/ instead of rebuilding the wheel"`
}

// PushResult is what the registry holds under the tag after the push, from which commit, and how long each step took.
type PushResult struct {
	Tag string `json:"tag"`
	// Source is the checkout that was built: the one asked for, the current folder or the one of the config file.
	Source   string `json:"source"`
	Revision string `json:"revision"`
	// Images lists every repository carrying the tag, including those an earlier full push of the tag left.
	Images []string `json:"images"`
	Steps  []Step   `json:"steps"`
	Error  string   `json:"error,omitempty"`
}

// keep is how long build cache entries and registry tags survive without any push using them: branches tested in parallel keep theirs, abandoned ones age out.
const keep = 72 * time.Hour

var slugRE = regexp.MustCompile(`[^a-z0-9._-]+`)

// Push builds with the repository's own tasks on the tool's builder, which pushes straight into the registry; a full push then drops the cache and the tags no push used for 3 days. It needs the tool up.
func (h *Host) Push(ctx context.Context, req PushRequest) (*PushResult, error) {
	source, err := h.source(req.Source)
	if err != nil {
		return nil, err
	}
	revision, err := gitRevision(ctx, source)
	if err != nil {
		return nil, err
	}
	if req.Tag == "" {
		if req.Tag, err = defaultTag(ctx, source); err != nil {
			return nil, err
		}
	}
	res := &PushResult{Tag: req.Tag, Source: source, Revision: revision, Images: []string{}, Steps: []Step{}}
	shared, err := h.lock(false, true)
	if err != nil {
		return nil, err
	}
	if err := h.requireUp(ctx); err != nil {
		shared.release()
		return nil, err
	}
	ok := h.build(ctx, source, revision, req, res)
	if ok {
		// The tag is dated before the lock goes, so no prune ever finds a tag just pushed stale.
		if res.Images, err = h.Images(ctx, req.Tag); err == nil {
			h.markPushed(res.Images)
		}
	}
	shared.release()
	if err != nil {
		return nil, err
	}
	if !ok {
		return res, nil
	}
	if len(res.Images) == 0 {
		res.Error = "the build pushed nothing tagged " + req.Tag
		return res, nil
	}
	if len(req.Targets) == 0 {
		steps, _ := h.pruneUnused(ctx)
		res.Steps = append(res.Steps, steps...)
	}
	return res, nil
}

func (h *Host) build(ctx context.Context, source, revision string, req PushRequest, res *PushResult) bool {
	if !req.SkipWheel {
		step, _ := h.step(ctx, "wheel", source, []string{"BRICKS_RELEASE_VERSION=" + req.Tag}, "task", "build:bricks")
		res.Steps = append(res.Steps, step)
		if !step.OK() {
			res.Error = "wheel build failed"
			return false
		}
	}
	env := []string{"BUILDX_BUILDER=" + BuilderName, "REGISTRY=" + RegistryPrefix, "IMAGE_TAG=" + req.Tag, "BASE_IMAGE_VERSION=" + req.Tag, "GITHUB_SHA=" + revision}
	args := append([]string{"build:containers", "PUSH=1", "--"}, req.Targets...)
	step, _ := h.step(ctx, "build and push", source, env, "task", args...)
	res.Steps = append(res.Steps, step)
	if !step.OK() {
		res.Error = "build failed"
		return false
	}
	return true
}

// markPushed dates each image's tag file to now: the registry rewrites it only when the content changes, and status reads it as the last push.
func (h *Host) markPushed(images []string) {
	now := time.Now()
	for _, ref := range images {
		repo, tag, _ := strings.Cut(ref, ":")
		_ = os.Chtimes(h.tagFile(repo, tag), now, now)
	}
}

// pruneUnused drops the stale build cache and tags unless a push, a prune or a board run holds the cache, which leaves them to the next up, down or full push. It returns its steps, the cache one first, and the tags it dropped.
func (h *Host) pruneUnused(ctx context.Context) ([]Step, []string) {
	exclusive, err := h.lock(true, false)
	if errors.Is(err, errBusy) {
		return []Step{{Name: "prune unused build cache and tags: skipped, a push or a board run is in progress"}}, nil
	}
	if err != nil {
		return []Step{{Name: "prune unused build cache and tags", Output: err.Error(), ExitCode: -1}}, nil
	}
	defer exclusive.release()
	tags, steps := h.dropStaleTags(ctx)
	return append([]Step{h.trimCache(ctx)}, steps...), tags
}

// trimCache drops the build cache no push used within keep; a full push rebuilds every container, so it refreshes everything its branch still needs. The caller holds the lock.
func (h *Host) trimCache(ctx context.Context) Step {
	step, _ := h.step(ctx, "prune unused build cache", "", nil, "docker", "buildx", "prune", "--builder", BuilderName, "--force",
		"--filter", fmt.Sprintf("until=%dh", int(keep.Hours())))
	return step
}

// dropStaleTags deletes the tags no push touched within keep and frees the layers only they used; a board run does not date a tag. The caller holds the lock and the registry runs.
func (h *Host) dropStaleTags(ctx context.Context) ([]string, []Step) {
	now := time.Now()
	var tags, refs []string
	for _, t := range h.tags() {
		if !t.Stale(now) {
			continue
		}
		tags = append(tags, t.Name)
		for _, repo := range t.Images {
			refs = append(refs, repo+":"+t.Name)
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	steps, err := h.deleteTags(ctx, refs)
	if err != nil {
		steps = append(steps, Step{Name: "wait for the registry", Output: err.Error(), ExitCode: 1})
	}
	return tags, steps
}

func gitRevision(ctx context.Context, dir string) (string, error) {
	head, err := gitOutput(ctx, dir, "rev-parse", "HEAD")
	if err != nil {
		return "", err
	}
	if _, err := gitOutput(ctx, dir, "diff", "--quiet", "HEAD", "--", ".", ":!dist"); err != nil {
		head += "-dirty"
	}
	return head, nil
}

// defaultTag is the branch name made safe for an image tag; a detached HEAD uses the worktree folder, so parallel worktrees never share a tag.
func defaultTag(ctx context.Context, dir string) (string, error) {
	branch, err := gitOutput(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if branch != "HEAD" {
		return Slug(branch), nil
	}
	top, err := gitOutput(ctx, dir, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", err
	}
	return "bt-" + Slug(filepath.Base(top)), nil
}

// Slug lowercases a name and replaces what an image tag cannot hold.
func Slug(name string) string {
	return strings.Trim(slugRE.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	out, code := Exec{}.Run(ctx, dir, nil, "git", args...)
	if code != 0 {
		return "", fmt.Errorf("git %s in %s: %s", strings.Join(args, " "), dir, strings.TrimSpace(out))
	}
	return strings.TrimSpace(out), nil
}
