// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// PushRequest builds the images of a source tree on the host and pushes them to the board registry.
type PushRequest struct {
	Source    string   `json:"source,omitempty" jsonschema:"app-bricks-py checkout to build from; default the current directory, whether it is a worktree is the caller's choice"`
	Tag       string   `json:"tag,omitempty" jsonschema:"image tag; default the branch name slugified, or bt-<short sha> on a detached HEAD"`
	Targets   []string `json:"targets,omitempty" jsonschema:"bake targets to build and push, default python-apps-base; add the runner of the brick under test when its compose references one"`
	SkipWheel bool     `json:"skip_wheel,omitempty" jsonschema:"reuse dist/ instead of rebuilding the wheel"`
}

// PushStep is one host-side step with its duration and the tail of what it printed.
type PushStep struct {
	Name     string  `json:"name"`
	Seconds  float64 `json:"seconds"`
	Output   string  `json:"output,omitempty"`
	ExitCode int     `json:"exit_code"`
}

// PushResult is what landed in the board registry and how long each step took.
type PushResult struct {
	Tag      string     `json:"tag"`
	Registry string     `json:"registry"`
	Revision string     `json:"revision"`
	Images   []string   `json:"images"`
	Steps    []PushStep `json:"steps"`
	Error    string     `json:"error,omitempty"`
}

var slugRE = regexp.MustCompile(`[^a-z0-9._-]+`)

// Push runs wheel, bake, push and verify, holding an SSH tunnel to the board registry for the duration.
func (b *Board) Push(ctx context.Context, req PushRequest) (*PushResult, error) {
	source, err := filepath.Abs(req.Source)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(filepath.Join(source, "docker-bake.hcl")); err != nil {
		return nil, fmt.Errorf("%s is not an app-bricks-py checkout: no docker-bake.hcl", source)
	}
	if len(req.Targets) == 0 {
		req.Targets = []string{"python-apps-base"}
	}
	revision, err := gitRevision(ctx, source)
	if err != nil {
		return nil, err
	}
	if req.Tag == "" {
		req.Tag, err = defaultTag(ctx, source, revision)
		if err != nil {
			return nil, err
		}
	}
	b.Dev = Dev{Registry: BoardRegistry, Tag: req.Tag}
	res := &PushResult{Tag: req.Tag, Registry: BoardRegistry, Revision: revision, Images: []string{}}

	if status, err := b.EnsureRegistry(ctx); err != nil {
		return nil, err
	} else if !status.Running {
		return nil, fmt.Errorf("board registry %s", status.Status)
	}

	tunnel, port, err := b.openTunnel(ctx)
	if err != nil {
		return nil, err
	}
	defer tunnel.close()
	local := fmt.Sprintf("localhost:%d/", port)

	host := hostRunner{dir: source}
	if !req.SkipWheel {
		step := host.step(ctx, "wheel", []string{"BRICKS_RELEASE_VERSION=" + req.Tag}, "task", "build:bricks")
		res.Steps = append(res.Steps, step)
		if step.ExitCode != 0 {
			res.Error = "wheel build failed"
			return res, nil
		}
	}
	// The repo's own local build task does the bake, so the flags live in one place.
	bakeEnv := []string{"REGISTRY=" + local, "IMAGE_TAG=" + req.Tag, "BASE_IMAGE_VERSION=" + req.Tag, "GITHUB_SHA=" + revision}
	bakeArgs := append([]string{"build:containers", "--"}, req.Targets...)
	step := host.step(ctx, "bake", bakeEnv, "task", bakeArgs...)
	res.Steps = append(res.Steps, step)
	if step.ExitCode != 0 {
		res.Error = "bake failed"
		return res, nil
	}
	for _, target := range req.Targets {
		ref := local + "app-bricks/" + target + ":" + req.Tag
		step := host.step(ctx, "push "+target, nil, "docker", "push", ref)
		res.Steps = append(res.Steps, step)
		if step.ExitCode != 0 {
			res.Error = "push of " + target + " failed"
			return res, nil
		}
		if err := verifyManifest(ctx, port, "app-bricks/"+target, req.Tag); err != nil {
			res.Error = err.Error()
			return res, nil
		}
		res.Images = append(res.Images, "app-bricks/"+target+":"+req.Tag)
		// The host copy is named after the tunnel port, which changes every push; the registry holds the image now.
		res.Steps = append(res.Steps, host.step(ctx, "untag host "+target, nil, "docker", "rmi", ref))
	}
	return res, nil
}

// tunnel is the ssh process forwarding a local port to the board registry.
type tunnel struct {
	cmd *exec.Cmd
}

func (t *tunnel) close() {
	_ = t.cmd.Process.Kill()
	_ = t.cmd.Wait()
}

// openTunnel forwards a free local port to the board registry until close.
func (b *Board) openTunnel(ctx context.Context) (*tunnel, int, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, 0, err
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	forward := fmt.Sprintf("%d:127.0.0.1:%s", port, RegistryPort)
	args := append(append([]string{}, sshOptions...), "-N", "-L", forward, b.Name)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, 0, fmt.Errorf("ssh tunnel: %w", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d/v2/", port)
	for i := 0; i < 30; i++ {
		if resp, err := http.Get(url); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return &tunnel{cmd: cmd}, port, nil
			}
		}
		if cmd.ProcessState != nil {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	cmd.Process.Kill()
	return nil, 0, fmt.Errorf("the board registry did not answer through the tunnel: %s", strings.TrimSpace(stderr.String()))
}

// verifyManifest checks the tag is really in the board registry, through the tunnel.
func verifyManifest(ctx context.Context, port int, repo, tag string) error {
	req, _ := http.NewRequestWithContext(ctx, http.MethodHead, fmt.Sprintf("http://127.0.0.1:%d/v2/%s/manifests/%s", port, repo, tag), nil)
	req.Header.Set("Accept", "application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s:%s is not in the board registry after the push (HTTP %d)", repo, tag, resp.StatusCode)
	}
	return nil
}

// hostRunner runs build commands on the developer machine with their output captured, never on stdout.
type hostRunner struct {
	dir string
}

func (h hostRunner) step(ctx context.Context, name string, env []string, command string, args ...string) PushStep {
	begin := time.Now()
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = h.dir
	cmd.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	step := PushStep{Name: name, Seconds: time.Since(begin).Seconds(), Output: tail(out.String(), 15)}
	if err != nil {
		step.ExitCode = 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			step.ExitCode = exitErr.ExitCode()
		}
	}
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

// defaultTag is the branch name made safe for an image tag, or bt-<short sha> on a detached HEAD.
func defaultTag(ctx context.Context, dir, revision string) (string, error) {
	branch, err := gitOutput(ctx, dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	if branch == "HEAD" {
		return "bt-" + strings.TrimSuffix(revision, "-dirty")[:8], nil
	}
	return Slug(branch), nil
}

// Slug lowercases a name and replaces what an image tag cannot hold.
func Slug(name string) string {
	return strings.Trim(slugRE.ReplaceAllString(strings.ToLower(name), "-"), "-")
}

func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s in %s: %w", strings.Join(args, " "), dir, err)
	}
	return strings.TrimSpace(string(out)), nil
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}
