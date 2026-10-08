// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package cli is the human entry point; every command maps to one operation on the board or the developer machine and prints its result.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/robgee86/arduino-test-buddy/internal/board"
	"github.com/robgee86/arduino-test-buddy/internal/host"
	"github.com/robgee86/arduino-test-buddy/internal/mcpserver"
	"github.com/robgee86/arduino-test-buddy/internal/version"
)

type options struct {
	board    string
	tag      string
	registry string
	format   string
	wait     time.Duration
}

// New builds the root command with every subcommand attached.
func New() *cobra.Command {
	opts := &options{}
	root := &cobra.Command{
		Use:           "arduino-test-buddy",
		Short:         "Run and clean up App Bricks tests on a board over SSH",
		Version:       version.Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	pf := root.PersistentFlags()
	pf.StringVar(&opts.board, "board", os.Getenv("ARDUINO_BOARD"), "SSH alias or user@host of the board (env ARDUINO_BOARD)")
	pf.StringVar(&opts.tag, "tag", os.Getenv("ARDUINO_BOARD_TAG"), "tag of the session's dev images; empty runs the released stack (env ARDUINO_BOARD_TAG)")
	pf.StringVar(&opts.registry, "registry", "", "registry prefix of the dev images; empty pulls from the host registry through a tunnel, "+board.LoadedRegistry+" for images loaded by hand")
	pf.StringVar(&opts.format, "format", "text", "output format: text or json")
	pf.DurationVar(&opts.wait, "wait", board.DefaultWait, "how long run and cleanup queue for their turn on a busy board")

	root.AddCommand(
		pushCmd(opts), registryCmd(opts), pruneCmd(opts), preflightCmd(opts), examplesCmd(opts), runCmd(opts), logsCmd(opts),
		execCmd(opts), shellCmd(opts), cleanupCmd(opts), mcpCmd(opts),
	)
	return root
}

func (o *options) target() (*board.Board, error) {
	if o.board == "" {
		return nil, fmt.Errorf("no board given: pass --board or set ARDUINO_BOARD")
	}
	b := board.New(o.board, board.SSH{Target: o.board}, board.Dev{Registry: o.registry, Tag: o.tag})
	b.Wait = o.wait
	return b, nil
}

// hostCmd runs one operation on the developer machine and prints its result.
func (o *options) hostCmd(use, short string, op func(context.Context, *host.Host) (any, error)) *cobra.Command {
	return &cobra.Command{
		Use:   use,
		Short: short,
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			h, err := host.New()
			if err != nil {
				return err
			}
			res, err := op(cmd.Context(), h)
			if err != nil {
				return err
			}
			return o.print(cmd.OutOrStdout(), res)
		},
	}
}

func (o *options) print(w io.Writer, v any) error {
	if o.format == "json" {
		enc := json.NewEncoder(w)
		enc.SetIndent("", "  ")
		return enc.Encode(v)
	}
	_, err := io.WriteString(w, text(v))
	return err
}

func preflightCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "preflight",
		Short: "Print the board state as facts: disk, CLI, apps, containers, images, models, devices",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := o.target()
			if err != nil {
				return err
			}
			res, err := b.Preflight(cmd.Context())
			if err != nil {
				return err
			}
			return o.print(cmd.OutOrStdout(), res)
		},
	}
}

func registryCmd(o *options) *cobra.Command {
	return o.hostCmd("registry", "Start the registry on this machine if needed and show where its data is, its size and its images",
		func(ctx context.Context, h *host.Host) (any, error) { return h.EnsureRegistry(ctx) })
}

func pushCmd(o *options) *cobra.Command {
	req := host.PushRequest{}
	cmd := o.hostCmd("push", "Build the wheel and the images of a checkout on this machine and push them to its registry",
		func(ctx context.Context, h *host.Host) (any, error) {
			req.Tag = o.tag
			return h.Push(ctx, req)
		})
	f := cmd.Flags()
	f.StringVar(&req.Source, "source", ".", "app-bricks-py checkout to build from")
	f.StringSliceVar(&req.Targets, "targets", nil, "bake targets to build and push, default every container")
	f.BoolVar(&req.SkipWheel, "skip-wheel", false, "reuse dist/ instead of rebuilding the wheel")
	return cmd
}

func pruneCmd(o *options) *cobra.Command {
	req := host.PruneRequest{}
	cmd := o.hostCmd("prune", "Give back disk on this machine: a tag's images (--tag), the build cache (--cache) or everything the tool created (--all)",
		func(ctx context.Context, h *host.Host) (any, error) {
			req.Tag = o.tag
			return h.Prune(ctx, req)
		})
	cmd.Flags().BoolVar(&req.Cache, "cache", false, "empty the tool's build cache")
	cmd.Flags().BoolVar(&req.All, "all", false, "remove the registry, its folder and the builder with its cache")
	return cmd
}

func examplesCmd(o *options) *cobra.Command {
	var brick string
	cmd := &cobra.Command{
		Use:   "examples",
		Short: "List the shipped examples that declare a brick, and which ones flash the MCU",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := o.target()
			if err != nil {
				return err
			}
			res, err := b.Examples(cmd.Context(), brick)
			if err != nil {
				return err
			}
			return o.print(cmd.OutOrStdout(), res)
		},
	}
	cmd.Flags().StringVar(&brick, "brick", "", "brick id without the arduino: prefix")
	return cmd
}

func runCmd(o *options) *cobra.Command {
	req := board.RunRequest{}
	cmd := &cobra.Command{
		Use:   "run [app|examples:<path>]",
		Short: "Start an app, wait for a marker in the log of this run, record its containers, stop it",
		Args:  cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) == 1 {
				req.App = args[0]
			}
			b, err := o.target()
			if err != nil {
				return err
			}
			res, err := b.Run(cmd.Context(), req)
			if err != nil {
				return err
			}
			return o.print(cmd.OutOrStdout(), res)
		},
	}
	f := cmd.Flags()
	f.StringVar(&req.LocalDir, "dir", "", "local app folder to copy to the board first")
	f.StringVar(&req.Marker, "marker", "", "regex that ends the run; default "+board.DefaultMarker+", or "+board.ExampleMarker+" for examples")
	f.DurationVar(&req.Timeout, "timeout", 2*time.Minute, "how long to wait for the marker")
	f.BoolVar(&req.KeepRunning, "keep", false, "leave the app running")
	f.BoolVar(&req.AllowFlash, "allow-flash", false, "allow an example with a sketch")
	return cmd
}

func logsCmd(o *options) *cobra.Command {
	var tail int
	var all bool
	cmd := &cobra.Command{
		Use:   "logs <app>",
		Short: "Print the app log, by default only the part since the last start",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := o.target()
			if err != nil {
				return err
			}
			log, err := b.Logs(cmd.Context(), args[0], tail, all)
			if err != nil {
				return err
			}
			_, err = io.WriteString(cmd.OutOrStdout(), log)
			return err
		},
	}
	cmd.Flags().IntVar(&tail, "tail", 400, "lines to fetch before cutting")
	cmd.Flags().BoolVar(&all, "all", false, "keep earlier runs too")
	return cmd
}

func execCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "exec <container> <command...>",
		Short: "Run a shell command inside a container on the board",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := o.target()
			if err != nil {
				return err
			}
			out, err := b.Exec(cmd.Context(), args[0], strings.Join(args[1:], " "))
			return o.printOutput(cmd, out, err)
		},
	}
}

func shellCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "shell <command...>",
		Short: "Run a shell command on the board's host OS",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := o.target()
			if err != nil {
				return err
			}
			out, err := b.Shell(cmd.Context(), strings.Join(args, " "))
			return o.printOutput(cmd, out, err)
		},
	}
}

func (o *options) printOutput(cmd *cobra.Command, out board.Output, err error) error {
	if err != nil {
		return err
	}
	if o.format == "json" {
		return o.print(cmd.OutOrStdout(), out)
	}
	io.WriteString(cmd.OutOrStdout(), out.Stdout)
	io.WriteString(cmd.ErrOrStderr(), out.Stderr)
	if !out.OK() {
		return fmt.Errorf("exit code %d", out.ExitCode)
	}
	return nil
}

func cleanupCmd(o *options) *cobra.Command {
	req := board.CleanupRequest{}
	cmd := &cobra.Command{
		Use:   "cleanup",
		Short: "Remove the session's apps, containers, networks, images and assets, then list what remains",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			b, err := o.target()
			if err != nil {
				return err
			}
			res, err := b.Cleanup(cmd.Context(), req)
			if err != nil {
				return err
			}
			return o.print(cmd.OutOrStdout(), res)
		},
	}
	f := cmd.Flags()
	f.StringVar(&req.Brick, "brick", "", "brick whose example containers and networks are removed")
	f.StringArrayVar(&req.Apps, "app", nil, "extra user app to destroy, repeatable")
	f.BoolVar(&req.DryRun, "dry-run", false, "list the steps without running them")
	return cmd
}

func mcpCmd(o *options) *cobra.Command {
	return &cobra.Command{
		Use:   "mcp",
		Short: "Serve the same operations as MCP tools over stdio",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			defaults := mcpserver.Defaults{Board: o.board, Tag: o.tag, Registry: o.registry, Wait: o.wait}
			return mcpserver.Serve(cmd.Context(), defaults)
		},
	}
}

// Execute runs the CLI and maps errors to the exit code.
func Execute(ctx context.Context) int {
	if err := New().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		return 1
	}
	return 0
}
