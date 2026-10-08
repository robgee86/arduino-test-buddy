// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"

	"github.com/arduino/arduino-test-buddy/internal/shell"
)

// Output is what a command printed on the board and how it ended.
type Output struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
}

// OK reports whether the command exited with zero.
func (o Output) OK() bool { return o.ExitCode == 0 }

// Runner executes shell scripts on a board and copies folders to it.
type Runner interface {
	Run(ctx context.Context, script string) (Output, error)
	Push(ctx context.Context, localDir, remoteParent string) error
}

// SSH runs scripts through the system ssh client so aliases, keys and jumps from the user's config apply.
type SSH struct {
	Target string
}

var sshOptions = []string{"-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}

// Run executes script on the board and returns its output; a non-zero exit is not a Go error.
func (s SSH) Run(ctx context.Context, script string) (Output, error) {
	args := append(append([]string{}, sshOptions...), s.Target, "--", script)
	cmd := exec.CommandContext(ctx, "ssh", args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	out := Output{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr *exec.ExitError
	switch {
	case err == nil:
	case errors.As(err, &exitErr):
		out.ExitCode = exitErr.ExitCode()
		if out.ExitCode == 255 {
			return out, fmt.Errorf("ssh to %s failed: %s", s.Target, bytes.TrimSpace(stderr.Bytes()))
		}
	default:
		return out, fmt.Errorf("ssh to %s: %w", s.Target, err)
	}
	return out, nil
}

// Push replaces remoteParent/<base of localDir> with a copy of localDir, streamed as a compressed tar.
func (s SSH) Push(ctx context.Context, localDir, remoteParent string) error {
	parent, base := filepath.Split(filepath.Clean(localDir))
	if parent == "" {
		parent = "."
	}
	remote := shell.Join("rm", "-rf", remoteParent+"/"+base) + " && " + shell.Join("mkdir", "-p", remoteParent) + " && " + shell.Join("tar", "xzf", "-", "-C", remoteParent)
	pipeline := shell.Join("tar", "czf", "-", "-C", parent, base) + " | " +
		shell.Join(append(append([]string{"ssh"}, sshOptions...), s.Target, "--", remote)...)
	cmd := exec.CommandContext(ctx, "sh", "-c", pipeline)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("push %s to %s:%s: %w: %s", localDir, s.Target, remoteParent, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return nil
}
