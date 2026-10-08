// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package mcpserver exposes the board operations as MCP tools over stdio; stdout is the protocol channel, so nothing else may print to it.
package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/arduino/arduino-test-buddy/internal/board"
	"github.com/arduino/arduino-test-buddy/internal/version"
)

// Defaults fill the target fields a tool call leaves empty.
type Defaults struct {
	Board    string
	Tag      string
	Registry string
}

// Target names the board and the dev images every tool acts on.
type Target struct {
	Board    string `json:"board,omitempty" jsonschema:"SSH alias or user@host of the board; defaults to the server's --board"`
	Tag      string `json:"tag,omitempty" jsonschema:"tag of the session's dev images; empty runs the released stack"`
	Registry string `json:"registry,omitempty" jsonschema:"registry prefix of the dev images; default the board registry localhost:5000/, dev.local/ for images loaded by hand"`
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
	return board.New(name, board.SSH{Target: name}, board.Dev{Registry: registry, Tag: tag}), nil
}

type preflightIn struct{ Target }

type registryIn struct{ Target }

type pushIn struct {
	Target
	Source    string   `json:"source,omitempty" jsonschema:"app-bricks-py checkout to build from; default the current directory, whether it is a worktree is the caller's choice"`
	Targets   []string `json:"targets,omitempty" jsonschema:"bake targets to build and push; default every container of the bake file, which costs a full build once per machine and a manifest check per unchanged image afterwards"`
	SkipWheel bool     `json:"skip_wheel,omitempty" jsonschema:"reuse dist/ instead of rebuilding the wheel"`
}

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
		Description: "Start the registry container on the board if it is not running and list the images it holds, with the session tag's ones apart.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in registryIn) (*mcp.CallToolResult, *board.RegistryStatus, error) {
		b, err := d.board(in.Target)
		if err != nil {
			return nil, nil, err
		}
		out, err := b.EnsureRegistry(ctx)
		return nil, out, err
	})

	mcp.AddTool(s, &mcp.Tool{
		Name:        "board_push",
		Description: "On the developer machine: build the wheel and every container image of an app-bricks-py checkout, then push them to the board registry through an SSH tunnel so only changed layers travel. Returns the tag and registry to pass to board_run, the git revision stamped into the images and per-step timings. The first push of a machine builds and moves everything, minutes and several GB; later pushes cost a manifest check per unchanged image.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in pushIn) (*mcp.CallToolResult, *board.PushResult, error) {
		b, err := d.board(in.Target)
		if err != nil {
			return nil, nil, err
		}
		out, err := b.Push(ctx, board.PushRequest{Source: in.Source, Tag: b.Dev.Tag, Targets: in.Targets, SkipWheel: in.SkipWheel})
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
		Description: "Copy an app folder if given, start the app or example with the session's dev images, wait until the marker appears in the log of this run or the timeout passes, record each container with its image and build revision, then stop the app unless keep_running. A failed start returns the start output and any brick variables it asked for.",
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
		Description: "Remove only what the session owns: bt-* apps and their folders, the containers with their volumes and the networks of bt-* apps and of the brick's examples, the images of the dev tag, and the assets folder of the tag last. Returns each step with its output and what the board still holds. Use dry_run to see the steps first.",
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
