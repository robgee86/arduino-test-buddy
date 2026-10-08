// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/arduino/arduino-test-buddy/internal/shell"
)

// DefaultMarker is the line a self-verifying test app prints when its checks are done.
const DefaultMarker = `BOARD-TEST SUMMARY`

// ExampleMarker is what the framework logs once App.run() is reached; shipped examples print nothing else predictable.
const ExampleMarker = `App started`

const (
	runBoundary  = "App is starting"
	pollInterval = 3 * time.Second
	logTail      = "400"
)

var (
	missingVariableRE = regexp.MustCompile(`variable "([^"]+)" is required by brick`)
	startEventRE      = regexp.MustCompile(`(?m)^\{.*\}$`)
)

// RunRequest describes one bounded run of an app or example.
type RunRequest struct {
	App         string        `json:"app" jsonschema:"app name under ArduinoApps, or examples:<path> for a shipped example"`
	LocalDir    string        `json:"local_dir,omitempty" jsonschema:"local app folder to copy to the board first; its base name becomes the app name"`
	Marker      string        `json:"marker,omitempty" jsonschema:"regex that ends the run when it appears in the log of this run; default BOARD-TEST SUMMARY"`
	Timeout     time.Duration `json:"-"`
	TimeoutS    int           `json:"timeout_seconds,omitempty" jsonschema:"seconds to wait for the marker after start; default 120"`
	KeepRunning bool          `json:"keep_running,omitempty" jsonschema:"leave the app running after the marker instead of stopping it"`
	AllowFlash  bool          `json:"allow_flash,omitempty" jsonschema:"allow an example with a sketch, which flashes the MCU"`
}

// Container is one container of the app after the run, with the revision its image was built from.
type Container struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	Revision string `json:"revision,omitempty"`
}

// RunResult is the evidence of one run: what started, what the log of this run says and what ran it.
type RunResult struct {
	App              string      `json:"app"`
	Started          bool        `json:"started"`
	StartLog         string      `json:"start_log"`
	Error            string      `json:"error,omitempty"`
	MissingVariables []string    `json:"missing_variables,omitempty"`
	Marker           string      `json:"marker"`
	MarkerFound      bool        `json:"marker_found"`
	TimedOut         bool        `json:"timed_out"`
	ElapsedS         float64     `json:"elapsed_seconds"`
	Log              string      `json:"log"`
	Containers       []Container `json:"containers"`
	Stopped          bool        `json:"stopped"`
}

// Run starts the app, waits for the marker in the log of this run only, records the containers and stops the app.
func (b *Board) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if req.LocalDir != "" {
		if _, err := os.Stat(filepath.Join(req.LocalDir, "app.yaml")); err != nil {
			return nil, fmt.Errorf("%s is not an app folder: no app.yaml", req.LocalDir)
		}
		name := filepath.Base(filepath.Clean(req.LocalDir))
		if req.App != "" && req.App != name {
			return nil, fmt.Errorf("app %q does not match the folder name %q", req.App, name)
		}
		req.App = name
		if err := b.runner.Push(ctx, req.LocalDir, AppsDir); err != nil {
			return nil, err
		}
	}
	if req.App == "" {
		return nil, fmt.Errorf("app name required")
	}
	if req.Marker == "" {
		req.Marker = DefaultMarker
		if IsExample(req.App) {
			req.Marker = ExampleMarker
		}
	}
	marker, err := regexp.Compile(req.Marker)
	if err != nil {
		return nil, fmt.Errorf("marker: %w", err)
	}
	timeout := req.Timeout
	if timeout == 0 && req.TimeoutS > 0 {
		timeout = time.Duration(req.TimeoutS) * time.Second
	}
	if timeout == 0 {
		timeout = 120 * time.Second
	}
	res := &RunResult{App: req.App, Marker: req.Marker, Containers: []Container{}}

	if IsExample(req.App) && !req.AllowFlash {
		out, err := b.run(ctx, shell.Join("test", "-d", examplePath(req.App)+"/sketch"))
		if err != nil {
			return nil, err
		}
		if out.OK() {
			res.Error = "example has a sketch and would flash the MCU; set allow_flash to run it"
			return res, nil
		}
	}

	begin := time.Now()
	start, err := b.run(ctx, b.appCLI(true, "app", "start", appRef(req.App), "--format", "json-lines"))
	if err != nil {
		return nil, err
	}
	res.StartLog, res.Started = parseStartEvent(start)
	if !res.Started {
		res.Error = strings.TrimSpace(start.Stderr)
		if res.Error == "" {
			res.Error = "app start exited with code " + strconv.Itoa(start.ExitCode)
		}
		res.MissingVariables = missingVariables(res.StartLog + "\n" + start.Stderr)
		res.ElapsedS = time.Since(begin).Seconds()
		return res, nil
	}

	deadline := time.Now().Add(timeout)
	poll := sectioned(map[string]string{
		"logs":  b.appCLI(true, "app", "logs", appRef(req.App), "--tail", logTail),
		"state": shell.Join("docker", "ps", "-a", "--filter", "name=^"+containerPrefix(req.App)+"-", "--format", "{{.State}}"),
	}, []string{"logs", "state"})
	for {
		out, err := b.run(ctx, poll)
		if err != nil {
			return nil, err
		}
		s := parseSections(out.Stdout)
		res.Log = LastRun(s["logs"])
		if marker.MatchString(res.Log) {
			res.MarkerFound = true
			break
		}
		if states := lines(s["state"]); len(states) > 0 && !slices.Contains(states, "running") {
			res.Error = "the app exited before the marker appeared"
			break
		}
		if time.Now().After(deadline) {
			res.TimedOut = true
			break
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	res.ElapsedS = time.Since(begin).Seconds()

	res.Containers, err = b.containers(ctx, req.App)
	if err != nil {
		return nil, err
	}
	if !req.KeepRunning {
		stop, err := b.run(ctx, b.appCLI(true, "app", "stop", appRef(req.App)))
		if err != nil {
			return nil, err
		}
		res.Stopped = stop.OK()
	}
	return res, nil
}

// Logs returns the app log, by default only the part written since the last start.
func (b *Board) Logs(ctx context.Context, app string, tail int, all bool) (string, error) {
	out, err := b.run(ctx, b.appCLI(true, "app", "logs", appRef(app), "--tail", strconv.Itoa(tail)))
	if err != nil {
		return "", err
	}
	if all {
		return out.Stdout, nil
	}
	return LastRun(out.Stdout), nil
}

// LastRun cuts a log that spans several starts of the same app down to the last one.
func LastRun(log string) string {
	idx := strings.LastIndex(log, runBoundary)
	if idx < 0 {
		return log
	}
	lineStart := strings.LastIndex(log[:idx], "\n") + 1
	return log[lineStart:]
}

func (b *Board) containers(ctx context.Context, app string) ([]Container, error) {
	format := `{{.Names}}|{{.Image}}|{{.Label "org.opencontainers.image.revision"}}`
	out, err := b.run(ctx, shell.Join("docker", "ps", "-a", "--filter", "name=^"+containerPrefix(app)+"-", "--format", format))
	if err != nil {
		return nil, err
	}
	containers := []Container{}
	for _, l := range lines(out.Stdout) {
		f := strings.SplitN(l, "|", 3)
		if len(f) == 3 {
			containers = append(containers, Container{Name: f[0], Image: f[1], Revision: f[2]})
		}
	}
	return containers, nil
}

// parseStartEvent reads the json-lines event of app start and returns its captured output and whether the app started.
func parseStartEvent(out Output) (string, bool) {
	var event struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Output struct {
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		} `json:"output"`
	}
	line := startEventRE.FindString(out.Stdout)
	if line == "" || json.Unmarshal([]byte(line), &event) != nil {
		return strings.TrimSpace(out.Stdout + "\n" + out.Stderr), false
	}
	if event.Error != "" {
		return event.Error, false
	}
	text := strings.TrimSpace(event.Output.Stdout + "\n" + event.Output.Stderr)
	return text, out.OK() && event.Status == "started"
}

// missingVariables lists the brick variables a failed start asked for; the fix is app brick config.
func missingVariables(text string) []string {
	var names []string
	for _, m := range missingVariableRE.FindAllStringSubmatch(text, -1) {
		if !slices.Contains(names, m[1]) {
			names = append(names, m[1])
		}
	}
	return names
}
