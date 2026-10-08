// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package host is the developer machine side: the image registry the boards pull from, the builder and their cache folder.
package host

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Names the tool owns on the developer machine; a fixed name means there is only ever one of each.
const (
	RegistryName  = "arduino-test-buddy-registry"
	RegistryImage = "registry:3"
	RegistryPort  = "5005"
	BuilderName   = "arduino-test-buddy"

	// RegistryPrefix is how the builder on the developer machine names the images it pushes.
	RegistryPrefix = "localhost:" + RegistryPort + "/"
)

// Step is one command on the developer machine with its duration and the tail of what it printed.
type Step struct {
	Name     string  `json:"name"`
	Seconds  float64 `json:"seconds"`
	Output   string  `json:"output,omitempty"`
	ExitCode int     `json:"exit_code"`
}

// OK reports whether the step exited with zero.
func (s Step) OK() bool { return s.ExitCode == 0 }

// Commander runs a command on the developer machine and returns everything it printed; nothing reaches stdout, which carries the MCP protocol.
type Commander interface {
	Run(ctx context.Context, dir string, env []string, name string, args ...string) (output string, exitCode int)
}

// Exec runs commands as child processes.
type Exec struct{}

// Run executes one command with env added to the current environment.
func (Exec) Run(ctx context.Context, dir string, env []string, name string, args ...string) (string, int) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), env...)
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	err := cmd.Run()
	var exitErr *exec.ExitError
	switch {
	case err == nil:
		return out.String(), 0
	case errors.As(err, &exitErr):
		return out.String(), exitErr.ExitCode()
	default:
		return out.String() + err.Error(), -1
	}
}

// Host is the registry, the builder and the cache folder holding the registry's data.
type Host struct {
	// Dir is the cache folder: deleting it with the registry stopped is always safe, everything in it rebuilds from a checkout.
	Dir string
	url string
	cmd Commander
}

// New uses the user cache folder of the platform, ~/Library/Caches on macOS and ~/.cache on Linux.
func New() (*Host, error) {
	cache, err := os.UserCacheDir()
	if err != nil {
		return nil, err
	}
	return &Host{Dir: filepath.Join(cache, "arduino-test-buddy"), url: "http://127.0.0.1:" + RegistryPort, cmd: Exec{}}, nil
}

// RegistryDir holds the registry's data, bind-mounted into its container.
func (h *Host) RegistryDir() string { return filepath.Join(h.Dir, "registry") }

// step runs one command and records it for the result.
func (h *Host) step(ctx context.Context, name, dir string, env []string, command string, args ...string) (Step, string) {
	begin := time.Now()
	out, code := h.cmd.Run(ctx, dir, env, command, args...)
	return Step{Name: name, Seconds: time.Since(begin).Seconds(), Output: tail(out, 15), ExitCode: code}, out
}

func tail(s string, n int) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n")
}

// dirSizeMB sums the files under dir; a missing folder is empty.
func dirSizeMB(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if info, err := d.Info(); err == nil {
				total += info.Size()
			}
		}
		return nil
	})
	return total / (1 << 20)
}
