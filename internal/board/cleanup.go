// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"maps"
	"regexp"
	"slices"
	"strings"

	"github.com/arduino/arduino-test-buddy/internal/shell"
)

// testPrefix marks every artifact a test session owns on a shared board.
const testPrefix = "bt-"

// CleanupRequest scopes a cleanup to one session: its apps, one brick's examples and the session's images.
type CleanupRequest struct {
	Brick  string   `json:"brick,omitempty" jsonschema:"brick whose example containers and networks are removed, e.g. video_objectdetection"`
	Apps   []string `json:"apps,omitempty" jsonschema:"extra user apps to destroy besides the bt-* ones"`
	DryRun bool     `json:"dry_run,omitempty" jsonschema:"list the steps without running them"`
}

// Step is one cleanup command and what it printed.
type Step struct {
	Description string  `json:"description"`
	Command     string  `json:"command"`
	Output      *Output `json:"output,omitempty"`
}

// Remaining is what the board still holds after cleanup, so the caller reads one table instead of five commands.
type Remaining struct {
	Apps             []string `json:"apps"`
	Containers       []string `json:"containers"`
	Networks         []string `json:"networks"`
	DevImages        []string `json:"dev_images"`
	RegistryImages   []string `json:"registry_images"`
	Assets           []string `json:"assets"`
	ExampleLeftovers []string `json:"example_leftovers"`
}

// CleanupReport lists what was removed and what remains.
type CleanupReport struct {
	Steps     []Step    `json:"steps"`
	Remaining Remaining `json:"remaining"`
	Warnings  []string  `json:"warnings"`
}

// Cleanup removes the session's artifacts and nothing else, then reports what is left.
func (b *Board) Cleanup(ctx context.Context, req CleanupRequest) (*CleanupReport, error) {
	state, err := b.inventory(ctx, req.Brick)
	if err != nil {
		return nil, err
	}
	report := &CleanupReport{Steps: []Step{}, Warnings: []string{}}
	if req.DryRun {
		report.Steps = append(b.appSteps(req, state), b.dockerSteps(req, state)...)
		report.Remaining = state
	} else {
		// Destroying an app removes its containers and network, so the docker phase plans from a fresh inventory.
		for _, phase := range []func(CleanupRequest, Remaining) []Step{b.appSteps, b.dockerSteps} {
			steps, err := b.execute(ctx, phase(req, state))
			if err != nil {
				return nil, err
			}
			report.Steps = append(report.Steps, steps...)
			if state, err = b.inventory(ctx, req.Brick); err != nil {
				return nil, err
			}
		}
		report.Remaining = state
	}
	if len(state.ExampleLeftovers) > 0 {
		report.Warnings = append(report.Warnings, "example folders hold data written by runs; they sit among shipped files and are left for you to decide")
	}
	if !b.Dev.Enabled() {
		report.Warnings = append(report.Warnings, "no dev tag given: images and assets were not touched")
	}
	return report, nil
}

func (b *Board) execute(ctx context.Context, steps []Step) ([]Step, error) {
	for i := range steps {
		out, err := b.run(ctx, steps[i].Command)
		if err != nil {
			return nil, err
		}
		steps[i].Output = &out
	}
	return steps, nil
}

// appSteps destroys the session's apps through the App CLI, which also removes their containers and networks.
func (b *Board) appSteps(req CleanupRequest, state Remaining) []Step {
	var steps []Step
	apps := map[string]bool{}
	for _, a := range req.Apps {
		apps[a] = true
	}
	for _, a := range state.Apps {
		if strings.HasPrefix(a, testPrefix) {
			apps[a] = true
		}
	}
	for _, a := range sorted(apps) {
		steps = append(steps,
			step("destroy app "+a, b.appCLI(false, "app", "destroy", appRef(a))),
			step("remove app folder "+a, shell.Join("rm", "-rf", AppsDir+"/"+a)))
	}
	return steps
}

// dockerSteps removes what the App CLI leaves behind, scoped by construction to the session's names, tag and registry.
func (b *Board) dockerSteps(req CleanupRequest, state Remaining) []Step {
	var steps []Step
	owned := sessionPattern(req.Brick)
	if names := matching(state.Containers, owned); len(names) > 0 {
		steps = append(steps, step("remove containers with their volumes", shell.Join(append([]string{"docker", "rm", "-f", "-v"}, names...)...)))
	}
	if names := matching(state.Networks, owned); len(names) > 0 {
		steps = append(steps, step("remove networks", shell.Join(append([]string{"docker", "network", "rm"}, names...)...)))
	}
	if b.Dev.Enabled() {
		if len(state.DevImages) > 0 {
			steps = append(steps, step("remove the session's images", shell.Join(append([]string{"docker", "rmi"}, state.DevImages...)...)))
		}
		for _, ref := range state.RegistryImages {
			steps = append(steps, step("delete "+ref+" from the board registry", registryDeleteScript(ref)))
		}
		for _, a := range state.Assets {
			steps = append(steps, step("remove assets folder "+a, shell.Join("rm", "-rf", AssetsDir+"/"+a)))
		}
	}
	return steps
}

func step(description, command string) Step {
	return Step{Description: description, Command: command}
}

// inventory lists the artifacts a session may own; the assets and images lists are already scoped to the session tag.
func (b *Board) inventory(ctx context.Context, brick string) (Remaining, error) {
	commands := map[string]string{
		"apps":       b.appCLI(false, "app", "ps", "-a", "--format", "json"),
		"containers": "docker ps -a --format '{{.Names}}'",
		"networks":   "docker network ls --format '{{.Name}}'",
		"images":     "docker images --format '{{.Repository}}:{{.Tag}}'",
		"assets":     shell.Join("ls", "-1", AssetsDir),
	}
	order := []string{"apps", "containers", "networks", "images", "assets"}
	if brick != "" {
		commands["leftovers"] = shell.Join("find", ExamplesDir+"/bricks/arduino/"+brick, "-mindepth", "2", "-maxdepth", "2", "(", "-name", "data", "-o", "-name", ".cache", ")")
		order = append(order, "leftovers")
	}
	out, err := b.run(ctx, sectioned(commands, order))
	if err != nil {
		return Remaining{}, err
	}
	s := parseSections(out.Stdout)
	state := Remaining{
		Apps:             []string{},
		Containers:       lines(s["containers"]),
		Networks:         userNetworks(lines(s["networks"])),
		DevImages:        []string{},
		Assets:           []string{},
		ExampleLeftovers: lines(s["leftovers"]),
	}
	apps, err := parseApps(s["apps"])
	if err != nil {
		return Remaining{}, err
	}
	for _, a := range apps {
		state.Apps = append(state.Apps, a.Name)
	}
	for _, ref := range lines(s["images"]) {
		if b.isDevImage(ref) {
			state.DevImages = append(state.DevImages, ref)
		}
	}
	for _, a := range lines(s["assets"]) {
		if b.Dev.Enabled() && a == b.Dev.Tag {
			state.Assets = append(state.Assets, a)
		}
	}
	if state.RegistryImages, err = b.registryImagesForTag(ctx); err != nil {
		return Remaining{}, err
	}
	if state.Containers == nil {
		state.Containers = []string{}
	}
	if state.Networks == nil {
		state.Networks = []string{}
	}
	if state.ExampleLeftovers == nil {
		state.ExampleLeftovers = []string{}
	}
	return state, nil
}

// userNetworks drops the networks Docker creates itself.
func userNetworks(names []string) []string {
	var out []string
	for _, n := range names {
		if n != "bridge" && n != "host" && n != "none" {
			out = append(out, n)
		}
	}
	return out
}

// sessionPattern matches the containers and networks of bt-* apps and, when given, of one brick's examples.
func sessionPattern(brick string) *regexp.Regexp {
	pattern := "^" + regexp.QuoteMeta(testPrefix)
	if brick != "" {
		pattern = "^(" + regexp.QuoteMeta(testPrefix) + "|" + regexp.QuoteMeta(containerPrefix("examples:bricks/arduino/"+brick)) + "-)"
	}
	return regexp.MustCompile(pattern)
}

func matching(names []string, re *regexp.Regexp) []string {
	var out []string
	for _, n := range names {
		if re.MatchString(n) {
			out = append(out, n)
		}
	}
	return out
}

func sorted(set map[string]bool) []string {
	return slices.Sorted(maps.Keys(set))
}
