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

	"github.com/robgee86/arduino-test-buddy/internal/shell"
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
	App         string        `json:"app" jsonschema:"examples:<path> for a shipped example, user:<name> or <name> for an app on the board stored as bt-<tag>-<name>, host:<folder> for an app folder on this machine, copied to the board first under its folder name"`
	Marker      string        `json:"marker,omitempty" jsonschema:"regex that ends the run when it appears in the log of this run; default BOARD-TEST SUMMARY"`
	Timeout     time.Duration `json:"-"`
	TimeoutS    int           `json:"timeout_seconds,omitempty" jsonschema:"seconds to wait for the marker after start; default 120"`
	KeepRunning bool          `json:"keep_running,omitempty" jsonschema:"leave the app running after the marker instead of stopping it"`
	AllowFlash  bool          `json:"allow_flash,omitempty" jsonschema:"allow an example with a sketch, which flashes the MCU"`
}

// Container is one container of the app after the run, with the revision its image was built from and, for a dev image pulled through the tunnel, the digest the board holds for it.
type Container struct {
	Name     string `json:"name"`
	Image    string `json:"image"`
	Revision string `json:"revision,omitempty"`
	Digest   string `json:"digest,omitempty"`
}

// RunResult is the evidence of one run: what started, what the log of this run says and what ran it.
type RunResult struct {
	App              string      `json:"app"`
	Registry         string      `json:"registry,omitempty"`
	WaitedS          float64     `json:"waited_for_board_seconds"`
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
	// DifferingImages are the dev images whose digest on the board is not the one the host registry serves for the tag: swapped under the tag, or not pulled again.
	DifferingImages []string `json:"differing_images,omitempty"`
	Stopped         bool     `json:"stopped"`
}

// Run waits for the session's turn on the board, starts the app, waits for the marker in the log of this run only, records the containers and stops the app.
func (b *Board) Run(ctx context.Context, req RunRequest) (*RunResult, error) {
	if dir, ok := strings.CutPrefix(req.App, hostPrefix); ok {
		dir, err := filepath.Abs(dir)
		if err != nil {
			return nil, err
		}
		if _, err := os.Stat(filepath.Join(dir, "app.yaml")); err != nil {
			return nil, fmt.Errorf("%s is not an app folder: no app.yaml", dir)
		}
		req.App = b.SessionApp(filepath.Base(dir))
		if err := b.runner.Push(ctx, dir, AppsDir+"/"+req.App); err != nil {
			return nil, err
		}
	}
	if req.App == "" {
		return nil, fmt.Errorf("app name required")
	}
	req.App = b.SessionApp(req.App)
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
	waited, err := b.withTurn(ctx, true, func(dev Dev) error {
		res.Registry = dev.Registry
		return b.run1(ctx, dev, req, marker, timeout, res)
	})
	res.WaitedS = waited
	if err != nil {
		return nil, err
	}
	return res, nil
}

// run1 is one run inside the session's turn.
func (b *Board) run1(ctx context.Context, dev Dev, req RunRequest, marker *regexp.Regexp, timeout time.Duration, res *RunResult) error {
	if IsExample(req.App) && !req.AllowFlash {
		out, err := b.run(ctx, shell.Join("test", "-d", examplePath(req.App)+"/sketch"))
		if err != nil {
			return err
		}
		if out.OK() {
			res.Error = "example has a sketch and would flash the MCU; set allow_flash to run it"
			return nil
		}
	}

	// A session's own app may still run after a crash or a kept run, so it restarts; an example running for another session must not be taken over.
	verb := "restart"
	if IsExample(req.App) {
		verb = "start"
	}
	begin := time.Now()
	start, err := b.run(ctx, appCLI(dev, "app", verb, appRef(req.App), "--format", "json-lines"))
	if err != nil {
		return err
	}
	res.StartLog, res.Started = parseStartEvent(start)
	if !res.Started {
		res.Error = strings.TrimSpace(start.Stderr)
		if res.Error == "" {
			res.Error = "app start exited with code " + strconv.Itoa(start.ExitCode)
		}
		res.MissingVariables = missingVariables(res.StartLog + "\n" + start.Stderr)
		res.ElapsedS = time.Since(begin).Seconds()
		return nil
	}

	deadline := time.Now().Add(timeout)
	poll := sectioned(map[string]string{
		"logs":  appCLI(dev, "app", "logs", appRef(req.App), "--tail", logTail),
		"state": shell.Join("docker", "ps", "-a", "--filter", "name=^"+containerPrefix(req.App)+"-", "--format", "{{.State}}"),
	}, []string{"logs", "state"})
	for {
		out, err := b.run(ctx, poll)
		if err != nil {
			return err
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
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
	res.ElapsedS = time.Since(begin).Seconds()

	if res.Containers, err = b.containers(ctx, req.App); err != nil {
		return err
	}
	if b.Dev.tunneled() {
		if res.DifferingImages, err = b.verifyImages(ctx, res.Containers); err != nil {
			return err
		}
	}
	if !req.KeepRunning {
		stop, err := b.run(ctx, appCLI(dev, "app", "stop", appRef(req.App)))
		if err != nil {
			return err
		}
		res.Stopped = stop.OK()
	}
	return nil
}

// verifyImages compares each dev image the app runs with the host registry: the digests the board holds for it must include the one the registry serves for the tag. It records that digest on the containers and returns the images that differ, swapped under the tag or not pulled again.
func (b *Board) verifyImages(ctx context.Context, containers []Container) ([]string, error) {
	var refs []string
	for _, c := range containers {
		if b.isDevImage(c.Image) && !slices.Contains(refs, c.Image) {
			refs = append(refs, c.Image)
		}
	}
	if len(refs) == 0 {
		return nil, nil
	}
	commands := map[string]string{}
	for _, ref := range refs {
		commands[ref] = shell.Join("docker", "image", "inspect", "--format", `{{join .RepoDigests " "}}`, ref)
	}
	out, err := b.run(ctx, sectioned(commands, refs))
	if err != nil {
		return nil, err
	}
	held := parseSections(out.Stdout)
	var differing []string
	for _, ref := range refs {
		_, repoTag, _ := strings.Cut(ref, "/")
		want, err := b.Host.Digest(ctx, repoTag)
		if err != nil {
			return nil, err
		}
		digest := heldDigest(held[ref], want)
		if digest != want || want == "" {
			differing = append(differing, ref)
		}
		for i := range containers {
			if containers[i].Image == ref {
				containers[i].Digest = digest
			}
		}
	}
	return differing, nil
}

// heldDigest picks, among the repo@digest references docker prints for an image, the wanted digest when the board holds it, else the first one.
func heldDigest(repoDigests, want string) string {
	digest := ""
	for _, field := range strings.Fields(repoDigests) {
		if _, d, ok := strings.Cut(field, "@"); ok {
			if d == want {
				return d
			}
			if digest == "" {
				digest = d
			}
		}
	}
	return digest
}

// Logs returns the app log, by default only the part written since the last start.
func (b *Board) Logs(ctx context.Context, app string, tail int, all bool) (string, error) {
	out, err := b.run(ctx, appCLI(b.Dev, "app", "logs", appRef(b.SessionApp(app)), "--tail", strconv.Itoa(tail)))
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
	return text, out.OK() && (event.Status == "started" || event.Status == "restarted")
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
