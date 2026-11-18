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
	read(t, "plugin/.claude-plugin/plugin.json", &manifest)
	if manifest.Version != released["."] {
		t.Errorf("the plugin says %s, the release %s: users see a plugin update only when its version moves with the release", manifest.Version, released["."])
	}
}

func TestTheMarketplaceOffersThePluginUnderItsOwnName(t *testing.T) {
	var manifest struct{ Name string }
	var marketplace struct {
		Plugins []struct{ Name, Source string }
	}
	read(t, "plugin/.claude-plugin/plugin.json", &manifest)
	read(t, ".claude-plugin/marketplace.json", &marketplace)
	if len(marketplace.Plugins) != 1 || marketplace.Plugins[0].Name != manifest.Name || marketplace.Plugins[0].Source != "./plugin" {
		t.Errorf("the marketplace must offer %q from ./plugin: %+v", manifest.Name, marketplace.Plugins)
	}
}

// longestTool is the room a tool name needs after the prefix Claude Code gives the tools of a plugin's server.
const longestTool = len("board_preflight")

func TestThePluginStartsTheInstalledBinaryAsItsServer(t *testing.T) {
	var manifest struct{ Name string }
	read(t, "plugin/.claude-plugin/plugin.json", &manifest)
	var servers struct {
		MCPServers map[string]struct {
			Command string
			Args    []string
		} `json:"mcpServers"`
	}
	read(t, "plugin/.mcp.json", &servers)
	if len(servers.MCPServers) != 1 {
		t.Fatalf("the plugin must define one server: %+v", servers.MCPServers)
	}
	for name, s := range servers.MCPServers {
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
	skill := filepath.Join(root, "plugin/skills/board-test")
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
