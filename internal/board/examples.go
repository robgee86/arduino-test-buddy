// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"encoding/base64"
	"strings"

	"github.com/robgee86/arduino-test-buddy/internal/shell"
)

// Example is a shipped example that declares a brick, with the facts that decide whether it can run.
type Example struct {
	ID        string `json:"id"`
	App       string `json:"app"`
	Name      string `json:"name"`
	HasSketch bool   `json:"has_sketch"`
}

// Examples lists the shipped examples of a brick, named by its id (video_object_detection) or its folder (video_objectdetection); an empty brick lists them all.
func (b *Board) Examples(ctx context.Context, brick string) ([]Example, error) {
	commands := map[string]string{
		"list":   b.appCLI(false, "app", "list", "--examples", "--format", "json"),
		"sketch": shell.Join("find", ExamplesDir, "-maxdepth", "5", "-type", "d", "-name", "sketch"),
	}
	order := []string{"list", "sketch"}
	if brick != "" {
		pattern := `^\s*-\s*arduino:` + brick + `\s*$`
		commands["brick"] = shell.Join("grep", "-rlE", "--include=app.yaml", pattern, ExamplesDir)
		order = append(order, "brick")
	}
	out, err := b.run(ctx, sectioned(commands, order))
	if err != nil {
		return nil, err
	}
	s := parseSections(out.Stdout)

	sketches := map[string]bool{}
	for _, l := range lines(s["sketch"]) {
		sketches[strings.TrimSuffix(l, "/sketch")] = true
	}
	declaring := map[string]bool{}
	for _, l := range lines(s["brick"]) {
		declaring[strings.TrimSuffix(l, "/app.yaml")] = true
	}

	catalog, err := parseApps(s["list"])
	if err != nil {
		return nil, err
	}
	examples := []Example{}
	for _, a := range catalog {
		app := DecodeAppID(a.ID)
		if !IsExample(app) {
			continue
		}
		dir := examplePath(app)
		if brick != "" && !declaring[dir] && !strings.Contains(app, "/"+brick+"/") {
			continue
		}
		examples = append(examples, Example{ID: a.ID, App: app, Name: a.Name, HasSketch: sketches[dir]})
	}
	return examples, nil
}

// DecodeAppID turns the catalog id, unpadded base64url of the app path, back into that path.
func DecodeAppID(id string) string {
	raw, err := base64.RawURLEncoding.DecodeString(id)
	if err != nil {
		return ""
	}
	return string(raw)
}
