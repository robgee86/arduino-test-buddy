// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package host

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
)

// Container states as the status reports them.
const (
	Running = "running"
	Stopped = "stopped"
	Missing = "missing"
)

// builderContainer is the container buildx runs the builder in.
const builderContainer = "buildx_buildkit_" + BuilderName + "0"

// ErrDown is returned by every operation that needs the registry or the builder while they are powered down.
var ErrDown = errors.New("arduino-test-buddy is down: run `arduino-test-buddy up` first")

// Status is whether the two containers run, where the data is and how much of it there is.
type Status struct {
	Registry string   `json:"registry"`
	Builder  string   `json:"builder"`
	URL      string   `json:"url"`
	Path     string   `json:"path"`
	SizeMB   int64    `json:"size_mb"`
	Cache    string   `json:"build_cache,omitempty"`
	Images   []string `json:"images"`
}

// Up reports whether both containers run.
func (s *Status) Up() bool { return s.Registry == Running && s.Builder == Running }

// Up creates or starts the registry and the builder. Neither restarts with Docker, so nothing runs until the next up.
func (h *Host) Up(ctx context.Context) (*Status, error) {
	registry, builder := h.state(ctx)
	switch registry {
	case Missing:
		if err := os.MkdirAll(h.RegistryDir(), 0o755); err != nil {
			return nil, err
		}
		// Running as the user keeps every file in the folder deletable without root.
		user := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
		if err := h.do(ctx, "start the registry", "docker", "run", "-d", "--name", RegistryName, "--user", user,
			"-p", "127.0.0.1:"+RegistryPort+":5000", "-v", h.RegistryDir()+":/var/lib/registry",
			"-e", "REGISTRY_STORAGE_DELETE_ENABLED=true", RegistryImage); err != nil {
			return nil, err
		}
	case Stopped:
		if err := h.do(ctx, "start the registry", "docker", "start", RegistryName); err != nil {
			return nil, err
		}
	}
	switch builder {
	case Missing:
		// Host networking lets the builder push to the registry on localhost.
		if err := h.do(ctx, "create the builder", "docker", "buildx", "create", "--name", BuilderName,
			"--driver", "docker-container", "--driver-opt", "network=host", "--bootstrap"); err != nil {
			return nil, err
		}
	case Stopped:
		if err := h.do(ctx, "start the builder", "docker", "buildx", "inspect", "--bootstrap", BuilderName); err != nil {
			return nil, err
		}
	}
	// Containers made before up/down existed restarted with Docker; this keeps every start explicit.
	if err := h.do(ctx, "disable automatic restarts", "docker", "update", "--restart=no", RegistryName, builderContainer); err != nil {
		return nil, err
	}
	if err := h.waitReady(ctx); err != nil {
		return nil, err
	}
	return h.Status(ctx)
}

// Down waits for running pushes, runs and prunes to end, then stops both containers; their data stays for the next up.
func (h *Host) Down(ctx context.Context) (*Status, error) {
	exclusive, err := h.lock(true, true)
	if err != nil {
		return nil, err
	}
	defer exclusive.release()
	registry, builder := h.state(ctx)
	if registry == Running {
		if err := h.do(ctx, "stop the registry", "docker", "stop", RegistryName); err != nil {
			return nil, err
		}
	}
	if builder == Running {
		if err := h.do(ctx, "stop the builder", "docker", "buildx", "stop", BuilderName); err != nil {
			return nil, err
		}
	}
	return h.Status(ctx)
}

// Status reads the state without starting anything.
func (h *Host) Status(ctx context.Context) (*Status, error) {
	st := &Status{URL: h.url, Path: h.RegistryDir(), SizeMB: dirSizeMB(h.RegistryDir()), Images: []string{}}
	st.Registry, st.Builder = h.state(ctx)
	if st.Registry == Running {
		images, err := h.Images(ctx, "")
		if err != nil {
			return nil, err
		}
		st.Images = images
	}
	if st.Builder == Running {
		out, _ := h.cmd.Run(ctx, "", nil, "docker", "buildx", "du", "--builder", BuilderName)
		st.Cache = field(out, "Total:")
	}
	return st, nil
}

// Use holds the registry for a board run, so a down waits for it, and fails at once when the tool is down.
func (h *Host) Use(ctx context.Context) (func(), error) {
	shared, err := h.lock(false, true)
	if err != nil {
		return nil, err
	}
	if registry, _ := h.state(ctx); registry != Running {
		shared.release()
		return nil, ErrDown
	}
	return shared.release, nil
}

// requireUp fails with ErrDown unless both containers run.
func (h *Host) requireUp(ctx context.Context) error {
	if registry, builder := h.state(ctx); registry != Running || builder != Running {
		return ErrDown
	}
	return nil
}

// state reads both containers without starting either; a status query never boots the builder.
func (h *Host) state(ctx context.Context) (registry, builder string) {
	out, _ := h.cmd.Run(ctx, "", nil, "docker", "ps", "-a", "--filter", "name=^"+RegistryName+"$", "--format", "{{.State}}")
	registry = containerState(strings.TrimSpace(out))
	out, code := h.cmd.Run(ctx, "", nil, "docker", "buildx", "inspect", BuilderName)
	builder = Missing
	if code == 0 {
		builder = containerState(field(out, "Status:"))
	}
	return registry, builder
}

func containerState(s string) string {
	switch s {
	case "":
		return Missing
	case Running:
		return Running
	default:
		return Stopped
	}
}

// field returns the value after the last line starting with key.
func field(out, key string) string {
	value := ""
	for _, l := range strings.Split(out, "\n") {
		if l = strings.TrimSpace(l); strings.HasPrefix(l, key) {
			value = strings.TrimSpace(strings.TrimPrefix(l, key))
		}
	}
	return value
}

// do runs one command and turns a failure into an error carrying its output.
func (h *Host) do(ctx context.Context, what, name string, args ...string) error {
	out, code := h.cmd.Run(ctx, "", nil, name, args...)
	if code != 0 {
		return fmt.Errorf("could not %s: %s", what, strings.TrimSpace(out))
	}
	return nil
}
