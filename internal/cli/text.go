// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/robgee86/arduino-test-buddy/internal/board"
	"github.com/robgee86/arduino-test-buddy/internal/host"
)

// text renders a result compactly for a terminal; anything unknown falls back to JSON.
func text(v any) string {
	var sb strings.Builder
	switch r := v.(type) {
	case *board.Preflight:
		fmt.Fprintf(&sb, "board: %s (%s)  cli: %s  disk free: %d MB\n", r.Board, r.Hostname, r.CLIVersion, r.DiskFreeMB)
		if r.Dev.Enabled() {
			fmt.Fprintf(&sb, "dev tag: %s  pulled: %s\n", r.Dev.Tag, orNone(r.DevImages))
		}
		fmt.Fprintf(&sb, "apps: %s\n", orNone(appNames(r.Apps)))
		fmt.Fprintf(&sb, "app folders: %s\n", orNone(r.AppFolders))
		sb.WriteString("containers:\n")
		for _, c := range r.Containers {
			fmt.Fprintf(&sb, "  %s  %s  %s\n", c.Name, c.Image, c.Status)
		}
		sb.WriteString("images:\n")
		for _, i := range r.Images {
			fmt.Fprintf(&sb, "  %s  %s\n", i.Ref, i.Size)
		}
		fmt.Fprintf(&sb, "models: %s\n", orNone(r.Models))
		fmt.Fprintf(&sb, "assets: %s\n", orNone(r.Assets))
		fmt.Fprintf(&sb, "video: %s\n", orNone(r.VideoDevices))
		fmt.Fprintf(&sb, "audio: %s\n", orNone(r.AudioCards))
	case *host.Status:
		power := "down"
		if r.Up() {
			power = "up"
		}
		fmt.Fprintf(&sb, "arduino-test-buddy is %s  registry: %s  builder: %s\n", power, r.Registry, r.Builder)
		fmt.Fprintf(&sb, "registry data: %s  (%d MB)\n", r.Path, r.SizeMB)
		if r.Cache != "" {
			fmt.Fprintf(&sb, "build cache: %s\n", r.Cache)
		}
		if r.Registry == host.Running {
			fmt.Fprintf(&sb, "images: %s\n", orNone(r.Images))
		}
	case *host.PushResult:
		fmt.Fprintf(&sb, "tag: %s  revision: %s\n", r.Tag, r.Revision)
		writeSteps(&sb, r.Steps)
		fmt.Fprintf(&sb, "images: %s\n", orNone(r.Images))
		if r.Error != "" && len(r.Steps) > 0 {
			fmt.Fprintf(&sb, "error: %s\n%s\n", r.Error, indent(r.Steps[len(r.Steps)-1].Output))
		} else if r.Error != "" {
			fmt.Fprintf(&sb, "error: %s\n", r.Error)
		}
		fmt.Fprintf(&sb, "run with: --tag %s\n", r.Tag)
	case *host.PruneResult:
		writeSteps(&sb, r.Steps)
		fmt.Fprintf(&sb, "freed: %d MB  registry data: %s (%d MB)\n", r.FreedMB, r.Path, r.SizeMB)
	case []board.Example:
		for _, e := range r {
			flash := ""
			if e.HasSketch {
				flash = "  [sketch: flashes the MCU]"
			}
			fmt.Fprintf(&sb, "%s  %q%s\n", e.App, e.Name, flash)
		}
		if len(r) == 0 {
			sb.WriteString("no examples\n")
		}
	case *board.RunResult:
		fmt.Fprintf(&sb, "app: %s  started: %t  marker found: %t  timed out: %t  stopped: %t  elapsed: %.0fs\n",
			r.App, r.Started, r.MarkerFound, r.TimedOut, r.Stopped, r.ElapsedS)
		if r.WaitedS >= 1 {
			fmt.Fprintf(&sb, "waited for the board: %.0fs\n", r.WaitedS)
		}
		if r.Error != "" {
			fmt.Fprintf(&sb, "error: %s\n", r.Error)
		}
		if len(r.MissingVariables) > 0 {
			fmt.Fprintf(&sb, "missing variables: %s  (fix: arduino-app-cli app brick config <app> <brick> NAME=value)\n", strings.Join(r.MissingVariables, ", "))
		}
		if !r.Started {
			fmt.Fprintf(&sb, "start output:\n%s\n", indent(r.StartLog))
		}
		if len(r.Containers) > 0 {
			sb.WriteString("containers:\n")
			for _, c := range r.Containers {
				fmt.Fprintf(&sb, "  %s  %s  %s\n", c.Name, c.Image, orDash(c.Revision))
			}
		}
		if r.Log != "" {
			fmt.Fprintf(&sb, "log of this run:\n%s\n", indent(r.Log))
		}
	case *board.CleanupReport:
		for _, s := range r.Steps {
			status := "planned"
			if s.Output != nil {
				status = fmt.Sprintf("exit %d", s.Output.ExitCode)
			}
			fmt.Fprintf(&sb, "%-7s %s\n        %s\n", status, s.Description, s.Command)
		}
		if len(r.Steps) == 0 {
			sb.WriteString("nothing to remove\n")
		}
		rem := r.Remaining
		fmt.Fprintf(&sb, "remaining: apps %s | containers %s | networks %s | dev images %s | assets %s\n",
			orNone(rem.Apps), orNone(rem.Containers), orNone(rem.Networks), orNone(rem.DevImages), orNone(rem.Assets))
		if len(rem.ExampleLeftovers) > 0 {
			fmt.Fprintf(&sb, "example leftovers: %s\n", strings.Join(rem.ExampleLeftovers, ", "))
		}
		for _, w := range r.Warnings {
			fmt.Fprintf(&sb, "warning: %s\n", w)
		}
	default:
		data, _ := json.MarshalIndent(v, "", "  ")
		sb.Write(data)
		sb.WriteString("\n")
	}
	return sb.String()
}

func writeSteps(sb *strings.Builder, steps []host.Step) {
	for _, s := range steps {
		fmt.Fprintf(sb, "  %-30s %6.1fs  exit %d\n", s.Name, s.Seconds, s.ExitCode)
	}
}

func appNames(apps []board.App) []string {
	names := make([]string, 0, len(apps))
	for _, a := range apps {
		names = append(names, a.Folder+"("+a.Status+")")
	}
	return names
}

func orNone(items []string) string {
	if len(items) == 0 {
		return "none"
	}
	return strings.Join(items, ", ")
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func indent(s string) string {
	return "  " + strings.ReplaceAll(strings.TrimRight(s, "\n"), "\n", "\n  ")
}
