// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/arduino/arduino-board-tool/internal/shell"
)

// App is one entry of the App CLI catalog.
type App struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Status  string `json:"status"`
	Example bool   `json:"example"`
}

// ContainerInfo is one container on the board.
type ContainerInfo struct {
	Name   string `json:"name"`
	Image  string `json:"image"`
	Status string `json:"status"`
}

// ImageInfo is one image on the board.
type ImageInfo struct {
	Ref  string `json:"ref"`
	Size string `json:"size"`
}

// Preflight is the state of a board before a test session, as facts rather than prose.
type Preflight struct {
	Board          string          `json:"board"`
	Hostname       string          `json:"hostname"`
	CLIVersion     string          `json:"cli_version"`
	DiskFreeMB     int64           `json:"disk_free_mb"`
	Dev            Dev             `json:"dev,omitempty"`
	DevImages      []string        `json:"dev_images"`
	RegistryImages []string        `json:"registry_images"`
	Apps           []App           `json:"apps"`
	AppFolders     []string        `json:"app_folders"`
	Containers     []ContainerInfo `json:"containers"`
	Images         []ImageInfo     `json:"images"`
	Models         []string        `json:"models"`
	Assets         []string        `json:"assets"`
	VideoDevices   []string        `json:"video_devices"`
	AudioCards     []string        `json:"audio_cards"`
}

// Preflight gathers the board state in one round trip.
func (b *Board) Preflight(ctx context.Context) (*Preflight, error) {
	order := []string{"hostname", "cli", "disk", "apps", "appdirs", "containers", "images", "models", "assets", "video", "audio"}
	script := sectioned(map[string]string{
		"hostname":   "hostname",
		"cli":        "arduino-app-cli version",
		"disk":       "df -k / | tail -1",
		"apps":       b.appCLI(false, "app", "ps", "-a", "--format", "json"),
		"appdirs":    shell.Join("ls", "-1", AppsDir),
		"containers": "docker ps -a --format '{{.Names}}|{{.Image}}|{{.Status}}'",
		"images":     "docker images --format '{{.Repository}}:{{.Tag}}|{{.Size}}'",
		"models":     shell.Join("ls", "-1", ModelsDir),
		"assets":     shell.Join("ls", "-1", AssetsDir),
		"video":      "ls -1 /dev/video*",
		"audio":      "grep -E '^ *[0-9]+ ' /proc/asound/cards",
	}, order)
	out, err := b.run(ctx, script)
	if err != nil {
		return nil, err
	}
	s := parseSections(out.Stdout)
	apps, err := parseApps(s["apps"])
	if err != nil {
		return nil, err
	}
	p := &Preflight{
		Board:        b.Name,
		Hostname:     strings.TrimSpace(s["hostname"]),
		CLIVersion:   firstLine(s["cli"]),
		DiskFreeMB:   diskFreeMB(s["disk"]),
		Dev:          b.Dev,
		Apps:         apps,
		AppFolders:   lines(s["appdirs"]),
		Models:       lines(s["models"]),
		Assets:       lines(s["assets"]),
		VideoDevices: lines(s["video"]),
		AudioCards:   lines(s["audio"]),
		DevImages:    []string{},
	}
	p.RegistryImages, err = b.registryImagesForTag(ctx)
	if err != nil {
		return nil, err
	}
	for _, l := range lines(s["containers"]) {
		f := strings.SplitN(l, "|", 3)
		if len(f) == 3 {
			p.Containers = append(p.Containers, ContainerInfo{Name: f[0], Image: f[1], Status: f[2]})
		}
	}
	for _, l := range lines(s["images"]) {
		f := strings.SplitN(l, "|", 2)
		if len(f) != 2 || !(strings.Contains(f[0], "app-bricks") || b.isDevImage(f[0])) {
			continue
		}
		p.Images = append(p.Images, ImageInfo{Ref: f[0], Size: f[1]})
		if b.isDevImage(f[0]) {
			p.DevImages = append(p.DevImages, f[0])
		}
	}
	return p, nil
}

// registryImagesForTag lists the board registry's entries carrying the session tag.
func (b *Board) registryImagesForTag(ctx context.Context) ([]string, error) {
	refs := []string{}
	if !b.Dev.Enabled() {
		return refs, nil
	}
	all, err := b.registryImages(ctx)
	if err != nil {
		return nil, err
	}
	for _, ref := range all {
		if strings.HasSuffix(ref, ":"+b.Dev.Tag) {
			refs = append(refs, ref)
		}
	}
	return refs, nil
}

// isDevImage matches only this session's registry and tag, never every dev image on a shared board.
func (b *Board) isDevImage(ref string) bool {
	return b.Dev.Enabled() && strings.HasPrefix(ref, b.Dev.registry()) && strings.HasSuffix(ref, ":"+b.Dev.Tag)
}

// parseApps reads the App CLI catalog; an unreadable catalog is an error, since cleanup decides what to destroy from it.
func parseApps(jsonText string) ([]App, error) {
	var catalog struct {
		Apps []App `json:"apps"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(jsonText)), &catalog); err != nil {
		return nil, fmt.Errorf("could not read the app catalog, is another arduino-app-cli still running? %w", err)
	}
	if catalog.Apps == nil {
		return []App{}, nil
	}
	return catalog.Apps, nil
}

func diskFreeMB(dfLine string) int64 {
	fields := strings.Fields(dfLine)
	if len(fields) < 4 {
		return 0
	}
	kb, _ := strconv.ParseInt(fields[3], 10, 64)
	return kb / 1024
}

func firstLine(s string) string {
	if ls := lines(s); len(ls) > 0 {
		return ls[0]
	}
	return ""
}
