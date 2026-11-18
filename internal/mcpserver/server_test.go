// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package mcpserver

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type noArgs struct{}

// session is a served connection seen from its two ends: the client, and what stops the server and tells how it ended.
type session struct {
	client *mcp.ClientSession
	stop   context.CancelFunc
	ended  <-chan error
}

// serveTool serves one tool to a client that listens for tool changes, which keeps a request open for the whole session as Claude Code does.
func serveTool(t *testing.T, grace time.Duration, tool func(ctx context.Context)) session {
	t.Helper()
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	mcp.AddTool(server, &mcp.Tool{Name: "work"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ noArgs) (*mcp.CallToolResult, any, error) {
		tool(ctx)
		return &mcp.CallToolResult{}, nil, nil
	})
	toServer, fromClient := io.Pipe()
	toClient, fromServer := io.Pipe()
	ctx, stop := context.WithCancel(context.Background())
	t.Cleanup(stop)
	ended := make(chan error, 1)
	go func() { ended <- serve(ctx, server, toServer, fromServer, grace) }()

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, &mcp.ClientOptions{
		ToolListChangedHandler: func(context.Context, *mcp.ToolListChangedRequest) {},
	})
	cs, err := client.Connect(context.Background(), &mcp.IOTransport{Reader: toClient, Writer: fromClient}, nil)
	if err != nil {
		t.Fatal(err)
	}
	return session{client: cs, stop: stop, ended: ended}
}

// end waits for the server to end and returns how it did.
func (s session) end(t *testing.T) error {
	t.Helper()
	select {
	case err := <-s.ended:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the server is still running")
		return nil
	}
}

// call starts the tool and returns once it runs.
func (s session) call(t *testing.T, running <-chan struct{}) {
	t.Helper()
	go s.client.CallTool(context.Background(), &mcp.CallToolParams{Name: "work"})
	select {
	case <-running:
	case <-time.After(5 * time.Second):
		t.Fatal("the tool never ran")
	}
}

func TestAStopEndsTheServerWhileTheClientListensForChanges(t *testing.T) {
	s := serveTool(t, time.Minute, func(context.Context) {})
	s.stop()
	if err := s.end(t); err != nil {
		t.Errorf("a stop is a clean end: %v", err)
	}
}

func TestAStopCancelsTheRunningCallAndWaitsForIt(t *testing.T) {
	running, returned := make(chan struct{}), make(chan struct{})
	s := serveTool(t, time.Minute, func(ctx context.Context) {
		close(running)
		<-ctx.Done()
		// What a call does to leave nothing behind, such as ending its child processes
		time.Sleep(50 * time.Millisecond)
		close(returned)
	})
	s.call(t, running)
	s.stop()
	if err := s.end(t); err != nil {
		t.Errorf("a stop is a clean end: %v", err)
	}
	select {
	case <-returned:
	default:
		t.Error("the server ended before its running call returned")
	}
}

func TestAStopDoesNotWaitLongForACallThatIgnoresIt(t *testing.T) {
	running, release := make(chan struct{}), make(chan struct{})
	t.Cleanup(func() { close(release) })
	s := serveTool(t, 50*time.Millisecond, func(context.Context) {
		close(running)
		<-release
	})
	s.call(t, running)
	s.stop()
	if err := s.end(t); err == nil {
		t.Error("ending with a call still running must be reported")
	}
}

func TestTheServerEndsWhenTheClientDisconnects(t *testing.T) {
	s := serveTool(t, time.Minute, func(context.Context) {})
	s.client.Close()
	if err := s.end(t); err != nil {
		t.Errorf("a client that leaves is a clean end: %v", err)
	}
}
