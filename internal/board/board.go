// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package board holds the operations a board test needs, each returning one result struct.
package board

import (
	"context"
	"strings"
	"sync"

	"github.com/robgee86/arduino-test-buddy/internal/shell"
)

// Paths owned by the App CLI on every supported board.
const (
	AppsDir     = "/home/arduino/ArduinoApps"
	ExamplesDir = "/var/lib/arduino-app-cli/examples"
	AssetsDir   = "/var/lib/arduino-app-cli/assets"
	ModelsDir   = "/var/lib/arduino-app-cli/models"

	// lockFile serializes every App CLI call across processes and hosts, the CLI corrupts the board when run twice.
	lockFile    = "/tmp/arduino-test-buddy.lock"
	lockTimeout = "900"

	// LoadedRegistry is the prefix for images loaded straight into the board's Docker: it never resolves, so a missing one fails instead of pulling a release.
	LoadedRegistry = "dev.local/"
)

// Dev names the development images a session runs on; an empty Tag means the released stack.
type Dev struct {
	Registry string `json:"registry,omitempty"`
	Tag      string `json:"tag,omitempty"`
}

// Enabled reports whether the session targets development images.
func (d Dev) Enabled() bool { return d.Tag != "" }

func (d Dev) registry() string {
	if d.Registry == "" {
		return BoardRegistry
	}
	return d.Registry
}

// Env returns the variables the App CLI reads to resolve the Python base image.
func (d Dev) Env() []string {
	if !d.Enabled() {
		return nil
	}
	return []string{
		"DOCKER_REGISTRY_BASE=" + d.registry(),
		"DOCKER_PYTHON_BASE_IMAGE=app-bricks/python-apps-base:" + d.Tag,
	}
}

// Board is one reachable board plus the images a session runs on.
type Board struct {
	Name   string
	Dev    Dev
	runner Runner
	mu     sync.Mutex
}

// New wires a board to its runner.
func New(name string, runner Runner, dev Dev) *Board {
	return &Board{Name: name, Dev: dev, runner: runner}
}

// run executes one script; calls from the same process never overlap.
func (b *Board) run(ctx context.Context, script string) (Output, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runner.Run(ctx, script)
}

// appCLI builds a serialized App CLI call; withDev adds the dev-image variables, which must be absent at cleanup since any call carrying them recreates the assets folder.
func (b *Board) appCLI(withDev bool, args ...string) string {
	words := []string{"flock", "-w", lockTimeout, lockFile}
	if withDev && b.Dev.Enabled() {
		words = append(words, "env")
		words = append(words, b.Dev.Env()...)
	}
	words = append(words, "arduino-app-cli")
	words = append(words, args...)
	return shell.Join(words...)
}

// IsExample reports whether app names a shipped example rather than a user app.
func IsExample(app string) bool { return strings.HasPrefix(app, "examples:") }

// appRef is the argument the App CLI resolves from any working directory: the example id as is, or the full folder of a user app.
func appRef(app string) string {
	if IsExample(app) || strings.HasPrefix(app, "/") {
		return app
	}
	return AppsDir + "/" + app
}

// examplePath returns the folder of an example on the board.
func examplePath(app string) string {
	return ExamplesDir + "/" + strings.TrimPrefix(app, "examples:")
}

// containerPrefix is how compose names the containers and networks of an app.
func containerPrefix(app string) string {
	if IsExample(app) {
		return "var-lib-arduino-app-cli-examples-" + strings.ReplaceAll(strings.TrimPrefix(app, "examples:"), "/", "-")
	}
	return app
}
