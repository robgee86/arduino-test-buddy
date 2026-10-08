// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/robgee86/arduino-test-buddy/internal/shell"
)

// Output is what a command printed on the board and how it ended.
type Output struct {
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr,omitempty"`
	ExitCode int    `json:"exit_code"`
}

// OK reports whether the command exited with zero.
func (o Output) OK() bool { return o.ExitCode == 0 }

// Runner executes shell scripts on a board, copies folders to it and takes turns on it.
type Runner interface {
	Run(ctx context.Context, script string) (Output, error)
	Push(ctx context.Context, localDir, remoteDir string) error
	Lease(ctx context.Context, hostPort string, wait time.Duration) (*Lease, error)
}

// Lease is one session's turn on the board, with the board-side port of the tunnel to the host registry when one was asked.
type Lease struct {
	Port    int
	WaitedS float64
	release func()
}

// Close ends the turn and the tunnel.
func (l *Lease) Close() {
	if l.release != nil {
		l.release()
	}
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

// Push replaces remoteDir with a copy of the contents of localDir, streamed as a compressed tar.
func (s SSH) Push(ctx context.Context, localDir, remoteDir string) error {
	remote := shell.Join("rm", "-rf", remoteDir) + " && " + shell.Join("mkdir", "-p", remoteDir) + " && " + shell.Join("tar", "xzf", "-", "-C", remoteDir)
	pipeline := shell.Join("tar", "czf", "-", "-C", localDir, ".") + " | " +
		shell.Join(append(append([]string{"ssh"}, sshOptions...), s.Target, "--", remote)...)
	cmd := exec.CommandContext(ctx, "sh", "-c", pipeline)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("push %s to %s:%s: %w: %s", localDir, s.Target, remoteDir, err, bytes.TrimSpace(stderr.Bytes()))
	}
	return nil
}

// Lease paths and markers on the board.
const (
	leaseFile   = "/tmp/arduino-test-buddy.lease"
	leaseMarker = "ARDUINO-TEST-BUDDY-LEASED"
)

// Lease waits for the board's turn file through an ssh session whose remote side holds it until its stdin closes. The tool owns that stdin, so the turn and the tunnel end when the tool exits for any reason, kill -9 included.
func (s SSH) Lease(ctx context.Context, hostPort string, wait time.Duration) (*Lease, error) {
	remote := shell.Join("exec", "flock", "-w", strconv.Itoa(int(wait.Seconds())), leaseFile,
		"sh", "-c", "echo "+leaseMarker+"; exec cat >/dev/null")
	var lastErr error
	for range 5 {
		port := 0
		args := append(append([]string{}, sshOptions...), "-o", "ExitOnForwardFailure=yes", "-o", "ServerAliveInterval=15", "-o", "ServerAliveCountMax=4")
		if hostPort != "" {
			// A fresh port each time, so a listener left by a dropped connection never blocks the next session.
			port = 20000 + rand.IntN(10000)
			args = append(args, "-R", fmt.Sprintf("%d:127.0.0.1:%s", port, hostPort))
		}
		lease, err := s.lease(ctx, append(args, s.Target, "--", remote), port)
		if err == nil {
			return lease, nil
		}
		lastErr = err
		if !strings.Contains(err.Error(), "remote port forwarding failed") {
			break
		}
	}
	return nil, lastErr
}

func (s SSH) lease(ctx context.Context, args []string, port int) (*Lease, error) {
	cmd := exec.CommandContext(ctx, "ssh", args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	begin := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("ssh to %s: %w", s.Target, err)
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if scanner.Text() == leaseMarker {
			release := func() {
				stdin.Close()
				done := make(chan struct{})
				go func() { _ = cmd.Wait(); close(done) }()
				select {
				case <-done:
				case <-time.After(10 * time.Second):
					_ = cmd.Process.Kill()
				}
			}
			return &Lease{Port: port, WaitedS: time.Since(begin).Seconds(), release: release}, nil
		}
	}
	_ = cmd.Wait()
	msg := strings.TrimSpace(stderr.String())
	if msg == "" {
		msg = "the board was not free within the wait time"
	}
	return nil, fmt.Errorf("taking a turn on %s: %s", s.Target, msg)
}
