// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mcpserver exposes the board operations as MCP tools over stdio; stdout is the protocol channel, so nothing else may print to it.
package mcpserver

import (
	"context"
	"fmt"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/robgee86/arduino-test-buddy/internal/board"
	"github.com/robgee86/arduino-test-buddy/internal/host"
	"github.com/robgee86/arduino-test-buddy/internal/version"
)

// Defaults fill the target fields a tool call leaves empty.
type Defaults struct {
	Board    string
	Tag      string
	Registry string
	Wait     time.Duration
}

// Target names the board and the dev images every tool acts on.
type Target struct {
	Board    string `json:"board,omitempty" jsonschema:"SSH alias or user@host of the board; defaults to the server's --board"`
	Tag      string `json:"tag,omitempty" jsonschema:"tag of the session's dev images; empty runs the released stack"`
	Registry string `json:"registry,omitempty" jsonschema:"registry prefix of the dev images; default the host registry through a tunnel, dev.local/ for images loaded by hand"`
}

func (d Defaults) board(t Target) (*board.Board, error) {
	name, tag, registry := t.Board, t.Tag, t.Registry
	if name == "" {
		name = d.Board
	}
	if tag == "" {
		tag = d.Tag
	}
	if registry == "" {
		registry = d.Registry
	}
	if name == "" {
		return nil, fmt.Errorf("no board given: pass board or start the server with --board")
	}
	b := board.New(name, board.SSH{Target: name}, board.Dev{Registry: registry, Tag: tag})
	if d.Wait > 0 {
		b.Wait = d.Wait
	}
	return b, nil
}

type preflightIn struct{ Target }

type registryIn struct{}

type examplesIn struct {
	Target
	Brick string `json:"brick,omitempty" jsonschema:"brick id without the arduino: prefix; empty lists every example"`
}

type examplesOut struct {
	Examples []board.Example `json:"examples"`
}

type runIn struct {
	Target
	board.RunRequest
}

type logsIn struct {
	Target
	App  string `json:"app" jsonschema:"app name or examples:<path>"`
	Tail int    `json:"tail,omitempty" jsonschema:"lines to fetch before cutting to the last run; default 400"`
	All  bool   `json:"all,omitempty" jsonschema:"keep earlier runs too"`
}

type logsOut struct {
	Log string `json:"log"`
}

type execIn struct {
	Target
	Container string `json:"container" jsonschema:"container name, see the run result or preflight"`
	Command   string `json:"command" jsonschema:"shell command run with sh -c inside the container"`
}

type shellIn struct {
	Target
	Command string `json:"command" jsonschema:"shell command run on the board's host OS; use the other tools for arduino-app-cli so calls stay serialized"`
}

type cleanupIn struct {
	Target
	board.CleanupRequest
}

// Serve registers the tools and blocks until the client disconnects.
func Serve(ctx context.Context, defaults Defaults) error {
	server := mcp.NewServer(&mcp.Implementation{
		Name:    "arduino-test-buddy",
		Title:   "Arduino Test Buddy",
		Version: version.Version,
	}, nil)
	register(server, defaults)
	return server.Run(ctx, &mcp.StdioTransport{})
}

func register(s *mcp.Server, d Defaults) {
	readOnly := &mcp.ToolAnnotations{ReadOnlyHint: true}
	destructive := &mcp.ToolAnnotations{DestructiveHint: ptr(true)}

	mcp.AddTool(s, &mcp.Tool{
		Name:        "board_preflight",
		Description: "Board state as facts before a test session: disk, CLI version, apps, app folders, containers, images, dev images present for the tag, models, assets, video devices and audio cards.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in preflightIn) (*mcp.CallToolResult, *board.Preflight, error) {
		b, err := d.board(in.Target)
		if err != nil {
			return nil, nil, err
		}
		out, err := b.Preflight(ctx)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "board_registry",
		Description: "On the developer machine: start the image registry the boards pull from if it is not running, and show the folder holding its data, its size and its images.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ registryIn) (*mcp.CallToolResult, *host.RegistryStatus, error) {
		h, err := host.New()
		if err != nil {
			return nil, nil, err
		}
		out, err := h.EnsureRegistry(ctx)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "board_push",
		Description: "On the developer machine: build the wheel and every container image of an app-bricks-py checkout and push them into the local registry, which every board pulls from during board_run. Returns the tag to pass to the board tools, the git revision stamped into the images and per-step timings. The first push of a machine builds everything, minutes and several GB of cache; later pushes rebuild only what changed. A full push also drops the build cache of images no longer built.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in host.PushRequest) (*mcp.CallToolResult, *host.PushResult, error) {
		h, err := host.New()
		if err != nil {
			return nil, nil, err
		}
		if in.Tag == "" {
			in.Tag = d.Tag
		}
		out, err := h.Push(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "board_prune",
		Description: "On the developer machine, after running pushes end: delete one tag's images from the registry and free the layers no other tag uses, empty the tool's build cache, or remove everything the tool created there. Touches nothing outside the tool's registry, folder and builder.",
		Annotations: destructive,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in host.PruneRequest) (*mcp.CallToolResult, *host.PruneResult, error) {
		h, err := host.New()
		if err != nil {
			return nil, nil, err
		}
		out, err := h.Prune(ctx, in)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "board_examples",
		Description: "Shipped examples whose app.yaml declares the brick, with the examples:<path> name to run each one and whether it has a sketch that flashes the MCU.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in examplesIn) (*mcp.CallToolResult, *examplesOut, error) {
		b, err := d.board(in.Target)
		if err != nil {
			return nil, nil, err
		}
		examples, err := b.Examples(ctx, in.Brick)
		if err != nil {
			return nil, nil, err
		}
		return nil, &examplesOut{Examples: examples}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "board_run",
		Description: "Wait for this session's turn on the board, copy an app folder if given, start the app or example with the session's dev images pulled from the host registry through a tunnel that lives only for the call, wait until the marker appears in the log of this run or the timeout passes, record each container with its image and build revision, then stop the app unless keep_running. Local apps are stored on the board as bt-<tag>-<name>, so sessions never collide. A failed start returns the start output and any brick variables it asked for.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in runIn) (*mcp.CallToolResult, *board.RunResult, error) {
		b, err := d.board(in.Target)
		if err != nil {
			return nil, nil, err
		}
		out, err := b.Run(ctx, in.RunRequest)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "board_logs",
		Description: "App log, by default only the lines written since the last start so an old traceback never shadows a healthy run.",
		Annotations: readOnly,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in logsIn) (*mcp.CallToolResult, *logsOut, error) {
		b, err := d.board(in.Target)
		if err != nil {
			return nil, nil, err
		}
		tail := in.Tail
		if tail == 0 {
			tail = 400
		}
		log, err := b.Logs(ctx, in.App, tail, in.All)
		if err != nil {
			return nil, nil, err
		}
		return nil, &logsOut{Log: log}, nil
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "board_exec",
		Description: "Run a shell command inside a container on the board and return stdout, stderr and the exit code.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in execIn) (*mcp.CallToolResult, *board.Output, error) {
		b, err := d.board(in.Target)
		if err != nil {
			return nil, nil, err
		}
		out, err := b.Exec(ctx, in.Container, in.Command)
		return nil, &out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "board_shell",
		Description: "Run a shell command on the board's host OS and return stdout, stderr and the exit code. Not for arduino-app-cli: the other tools serialize those calls.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in shellIn) (*mcp.CallToolResult, *board.Output, error) {
		b, err := d.board(in.Target)
		if err != nil {
			return nil, nil, err
		}
		out, err := b.Shell(ctx, in.Command)
		return nil, &out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "board_cleanup",
		Description: "Wait for this session's turn, then remove only what it owns: its bt-<tag>-* apps and their folders, the containers with their volumes and the networks of those apps and of the brick's examples unless another session kept one running, the images of the tag pulled on the board, and the assets folder of the tag, last. Returns each step with its output and what the board still holds. Use dry_run to see the steps first.",
		Annotations: destructive,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in cleanupIn) (*mcp.CallToolResult, *board.CleanupReport, error) {
		b, err := d.board(in.Target)
		if err != nil {
			return nil, nil, err
		}
		out, err := b.Cleanup(ctx, in.CleanupRequest)
		return nil, out, err
	})
}

func ptr[T any](v T) *T { return &v }
