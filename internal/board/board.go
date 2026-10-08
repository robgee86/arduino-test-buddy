// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package board holds the operations a board test needs, each returning one result struct.
package board

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/robgee86/arduino-test-buddy/internal/host"
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

// Dev names the development images a session runs on; an empty Tag means the released stack, an empty Registry the host registry through a tunnel.
type Dev struct {
	Registry string `json:"registry,omitempty"`
	Tag      string `json:"tag,omitempty"`
}

// Enabled reports whether the session targets development images.
func (d Dev) Enabled() bool { return d.Tag != "" }

// tunneled reports whether the images come from the host registry, reached through the lease's tunnel.
func (d Dev) tunneled() bool { return d.Enabled() && d.Registry == "" }

// Env returns the variables the App CLI reads to resolve the Python base image; outside a lease the host registry has no port on the board, which only matters to calls that pull.
func (d Dev) Env() []string {
	if !d.Enabled() {
		return nil
	}
	registry := d.Registry
	if registry == "" {
		registry = host.RegistryPrefix
	}
	return []string{
		"DOCKER_REGISTRY_BASE=" + registry,
		"DOCKER_PYTHON_BASE_IMAGE=app-bricks/python-apps-base:" + d.Tag,
	}
}

// DefaultWait is how long a session queues for its turn on a busy board.
const DefaultWait = 30 * time.Minute

// HostRegistry is the registry a turn's tunnel leads to; Use holds it for the run so it can't be powered down mid-pull.
type HostRegistry interface {
	Use(ctx context.Context) (release func(), err error)
}

// Board is one reachable board plus the images a session runs on.
type Board struct {
	Name   string
	Dev    Dev
	Wait   time.Duration
	Host   HostRegistry
	runner Runner
	mu     sync.Mutex
}

// New wires a board to its runner.
func New(name string, runner Runner, dev Dev) *Board {
	return &Board{Name: name, Dev: dev, Wait: DefaultWait, runner: runner}
}

// withTurn runs fn while the session holds the board; with forward, the images are pulled from the host registry through the tunnel of the turn.
func (b *Board) withTurn(ctx context.Context, forward bool, fn func(dev Dev) error) (float64, error) {
	hostPort := ""
	if forward && b.Dev.tunneled() {
		hostPort = host.RegistryPort
		if b.Host != nil {
			release, err := b.Host.Use(ctx)
			if err != nil {
				return 0, err
			}
			defer release()
		}
	}
	lease, err := b.runner.Lease(ctx, hostPort, b.Wait)
	if err != nil {
		return 0, err
	}
	defer lease.Close()
	dev := b.Dev
	if lease.Port != 0 {
		dev.Registry = fmt.Sprintf("localhost:%d/", lease.Port)
	}
	return lease.WaitedS, fn(dev)
}

// run executes one script; calls from the same process never overlap.
func (b *Board) run(ctx context.Context, script string) (Output, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.runner.Run(ctx, script)
}

// appCLI builds a serialized App CLI call with the variables of dev; cleanup passes none, since any call carrying them recreates the assets folder.
func appCLI(dev Dev, args ...string) string {
	words := []string{"flock", "-w", lockTimeout, lockFile}
	if env := dev.Env(); env != nil {
		words = append(append(words, "env"), env...)
	}
	words = append(words, "arduino-app-cli")
	words = append(words, args...)
	return shell.Join(words...)
}

// SessionApp is the board name of a local test app, bt-<tag>-<name> or bt-released-<name> without a tag, so sessions sharing a board never collide on or remove each other's apps.
func (b *Board) SessionApp(name string) string {
	if IsExample(name) || strings.HasPrefix(name, b.sessionPrefix()) {
		return name
	}
	return b.sessionPrefix() + strings.TrimPrefix(name, testPrefix)
}

// releasedSession names the session of a run on the released stack, so it never sweeps a tagged session's apps.
const releasedSession = "released"

// sessionPrefix starts every app the session owns.
func (b *Board) sessionPrefix() string {
	session := releasedSession
	if b.Dev.Enabled() {
		session = strings.ReplaceAll(strings.TrimPrefix(b.Dev.Tag, testPrefix), ".", "-")
	}
	return testPrefix + session + "-"
}

// isDevImage matches only this session's tag, under its registry prefix or any tunnel port of the host registry.
func (b *Board) isDevImage(ref string) bool {
	if !b.Dev.Enabled() || !strings.HasSuffix(ref, ":"+b.Dev.Tag) {
		return false
	}
	if b.Dev.tunneled() {
		return tunnelImageRE.MatchString(ref)
	}
	return strings.HasPrefix(ref, b.Dev.Registry)
}

var tunnelImageRE = regexp.MustCompile(`^localhost:[0-9]+/app-bricks/`)

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
