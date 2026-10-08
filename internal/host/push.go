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
	Source    string   `json:"source,omitempty" jsonschema:"app-bricks-py checkout to build from; default the current directory, whether it is a worktree is the caller's choice"`
	Tag       string   `json:"tag,omitempty" jsonschema:"image tag; default the branch name, or bt-<worktree folder> on a detached HEAD"`
	Targets   []string `json:"targets,omitempty" jsonschema:"bake targets to build and push; default every container, which also prunes the build cache of images no longer built"`
	SkipWheel bool     `json:"skip_wheel,omitempty" jsonschema:"reuse dist/ instead of rebuilding the wheel"`
}

// PushResult is what the registry holds under the tag after the push, from which commit, and how long each step took.
type PushResult struct {
	Tag      string `json:"tag"`
	Revision string `json:"revision"`
	// Images lists every repository carrying the tag, including those an earlier full push of the tag left.
	Images []string `json:"images"`
	Steps  []Step   `json:"steps"`
	Error  string   `json:"error,omitempty"`
}

// cacheMargin keeps the cache entries a push touched just before its own start.
const cacheMargin = time.Minute

var slugRE = regexp.MustCompile(`[^a-z0-9._-]+`)

// Push builds with the repository's own tasks on the tool's builder, which pushes straight into the registry; a full push then drops the cache it did not use. It needs the tool up.
func (h *Host) Push(ctx context.Context, req PushRequest) (*PushResult, error) {
	source, err := filepath.Abs(req.Source)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(source, "docker-bake.hcl")); err != nil {
		return nil, fmt.Errorf("%s is not an app-bricks-py checkout: no docker-bake.hcl", source)
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
	res := &PushResult{Tag: req.Tag, Revision: revision, Images: []string{}, Steps: []Step{}}
	shared, err := h.lock(false, true)
	if err != nil {
		return nil, err
	}
	if err := h.requireUp(ctx); err != nil {
		shared.release()
		return nil, err
	}
	begin := time.Now()
	ok := h.build(ctx, source, revision, req, res)
	shared.release()
	if !ok {
		return res, nil
	}
	if res.Images, err = h.Images(ctx, req.Tag); err != nil {
		return nil, err
	}
	if len(res.Images) == 0 {
		res.Error = "the build pushed nothing tagged " + req.Tag
		return res, nil
	}
	if len(req.Targets) == 0 {
		res.Steps = append(res.Steps, h.pruneUnused(ctx, time.Since(begin)+cacheMargin))
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

// pruneUnused drops the build cache a full push did not touch: everything still needed was used by it, the rest belongs to images no longer built.
func (h *Host) pruneUnused(ctx context.Context, used time.Duration) Step {
	exclusive, err := h.lock(true, false)
	if errors.Is(err, errBusy) {
		return Step{Name: "prune unused build cache: skipped, another push is running and prunes when it ends"}
	}
	if err != nil {
		return Step{Name: "prune unused build cache", Output: err.Error(), ExitCode: -1}
	}
	defer exclusive.release()
	step, _ := h.step(ctx, "prune unused build cache", "", nil, "docker", "buildx", "prune", "--builder", BuilderName, "--force",
		"--filter", fmt.Sprintf("until=%ds", int(used.Seconds())))
	return step
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
