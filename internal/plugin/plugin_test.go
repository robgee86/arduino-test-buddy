// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

// Package plugin checks the Claude Code plugin shipped in the repository against the release it belongs to.
package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

const root = "../.."

func read(t *testing.T, path string, v any) {
	data, err := os.ReadFile(filepath.Join(root, path))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, v); err != nil {
		t.Fatalf("%s: %v", path, err)
	}
}

func TestThePluginCarriesTheVersionOfTheRelease(t *testing.T) {
	var released map[string]string
	var manifest struct{ Name, Version string }
	read(t, ".release-please-manifest.json", &released)
	read(t, ".claude-plugin/plugin.json", &manifest)
	if manifest.Version != released["."] {
		t.Errorf("the plugin says %s, the release %s: users see a plugin update only when its version moves with the release", manifest.Version, released["."])
	}
}

func TestTheRepositoryIsAPluginAndNotACatalog(t *testing.T) {
	// The catalog lives in its own repository and names this one as the source: a second catalog here would offer the same plugin twice
	if _, err := os.Stat(filepath.Join(root, ".claude-plugin", "marketplace.json")); err == nil {
		t.Error("the catalog belongs to the arduino-claude-plugins repository, not to this one")
	}
}

// longestTool is the room a tool name needs after the prefix Claude Code gives the tools of a plugin's server.
const longestTool = len("board_preflight")

func TestThePluginStartsTheInstalledBinaryAsItsServer(t *testing.T) {
	// The server is declared in the manifest: a .mcp.json at the root would also register it for whoever opens this repository
	var manifest struct {
		Name       string
		MCPServers map[string]struct {
			Command string
			Args    []string
		} `json:"mcpServers"`
	}
	read(t, ".claude-plugin/plugin.json", &manifest)
	if _, err := os.Stat(filepath.Join(root, ".mcp.json")); err == nil {
		t.Error("a .mcp.json at the root registers the server for every session in this repository")
	}
	if len(manifest.MCPServers) != 1 {
		t.Fatalf("the plugin must define one server: %+v", manifest.MCPServers)
	}
	for name, s := range manifest.MCPServers {
		if s.Command != "arduino-test-buddy" || len(s.Args) != 1 || s.Args[0] != "mcp" {
			t.Errorf("the plugin must run `arduino-test-buddy mcp`: %+v", s)
		}
		// A tool name holds 64 characters at most, and a longer one never reaches the model
		if full := len("mcp__plugin_"+manifest.Name+"_"+name+"__") + longestTool; full > 64 {
			t.Errorf("server %q makes tool names of %d characters, over the 64 a tool name may have", name, full)
		}
	}
}

func TestTheSkillShipsEveryFileItLinks(t *testing.T) {
	skill := filepath.Join(root, "skills/board-test")
	text, err := os.ReadFile(filepath.Join(skill, "SKILL.md"))
	if err != nil {
		t.Fatal(err)
	}
	links := regexp.MustCompile(`\]\(([^)#:]+)\)`).FindAllSubmatch(text, -1)
	if len(links) == 0 {
		t.Fatal("the skill links no supporting file")
	}
	for _, l := range links {
		if _, err := os.Stat(filepath.Join(skill, string(l[1]))); err != nil {
			t.Errorf("the skill links %s, which the plugin does not ship", l[1])
		}
	}
	if _, err := os.Stat(filepath.Join(skill, "boards.local.yaml")); err == nil {
		t.Error("a developer's own boards file must never ship with the plugin")
	}
}
