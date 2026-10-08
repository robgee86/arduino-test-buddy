// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/arduino/arduino-test-buddy/internal/shell"
)

// The registry on the board: bound to loopback, so only the board's daemon and an SSH tunnel reach it.
const (
	RegistryName  = "arduino-test-buddy-registry"
	RegistryImage = "registry:3"
	RegistryPort  = "5000"
	registryURL   = "http://127.0.0.1:" + RegistryPort

	// BoardRegistry is the prefix the board's daemon pulls session images from.
	BoardRegistry = "localhost:" + RegistryPort + "/"
)

// RegistryStatus is the state of the board registry and what it holds for the session tag.
type RegistryStatus struct {
	Running   bool     `json:"running"`
	Created   bool     `json:"created"`
	Status    string   `json:"status"`
	Images    []string `json:"images"`
	AllImages []string `json:"all_images"`
}

// EnsureRegistry starts the board registry if needed and reports its catalog.
func (b *Board) EnsureRegistry(ctx context.Context) (*RegistryStatus, error) {
	status := &RegistryStatus{Images: []string{}, AllImages: []string{}}
	state, err := b.run(ctx, shell.Join("docker", "ps", "-a", "--filter", "name=^"+RegistryName+"$", "--format", "{{.State}}"))
	if err != nil {
		return nil, err
	}
	switch strings.TrimSpace(state.Stdout) {
	case "running":
	case "":
		out, err := b.run(ctx, shell.Join("docker", "run", "-d", "--name", RegistryName, "--restart", "unless-stopped",
			"-p", "127.0.0.1:"+RegistryPort+":"+RegistryPort, "-v", RegistryName+":/var/lib/registry",
			"-e", "REGISTRY_STORAGE_DELETE_ENABLED=true", RegistryImage))
		if err != nil {
			return nil, err
		}
		if !out.OK() {
			return nil, fmt.Errorf("could not start the registry: %s", strings.TrimSpace(out.Stderr))
		}
		status.Created = true
	default:
		if out, err := b.run(ctx, shell.Join("docker", "start", RegistryName)); err != nil || !out.OK() {
			return nil, fmt.Errorf("could not restart the registry: %s", strings.TrimSpace(out.Stderr))
		}
	}
	ready, err := b.run(ctx, "for i in $(seq 1 30); do curl -sf "+registryURL+"/v2/ >/dev/null && exit 0; sleep 1; done; exit 1")
	if err != nil {
		return nil, err
	}
	status.Running = ready.OK()
	status.Status = "running"
	if !status.Running {
		status.Status = "not answering on " + registryURL
		return status, nil
	}
	status.AllImages, err = b.registryImages(ctx)
	if err != nil {
		return nil, err
	}
	for _, ref := range status.AllImages {
		if b.Dev.Enabled() && strings.HasSuffix(ref, ":"+b.Dev.Tag) {
			status.Images = append(status.Images, ref)
		}
	}
	return status, nil
}

// registryImages lists every repository:tag the board registry holds, empty when it is not running.
func (b *Board) registryImages(ctx context.Context) ([]string, error) {
	out, err := b.run(ctx, "curl -sf --max-time 5 "+registryURL+"/v2/_catalog")
	if err != nil {
		return nil, err
	}
	refs := []string{}
	if !out.OK() {
		return refs, nil
	}
	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if json.Unmarshal([]byte(out.Stdout), &catalog) != nil || len(catalog.Repositories) == 0 {
		return refs, nil
	}
	var parts []string
	for _, repo := range catalog.Repositories {
		parts = append(parts, shell.Join("echo", sectionMarker+repo), "curl -sf --max-time 5 "+registryURL+"/v2/"+repo+"/tags/list")
	}
	tags, err := b.run(ctx, shell.Script(parts...))
	if err != nil {
		return nil, err
	}
	for repo, body := range parseSections(tags.Stdout) {
		var list struct {
			Tags []string `json:"tags"`
		}
		if json.Unmarshal([]byte(body), &list) != nil {
			continue
		}
		for _, t := range list.Tags {
			refs = append(refs, repo+":"+t)
		}
	}
	return refs, nil
}

// registryDeleteScript removes one tag from the board registry; registry 3 deletes by tag, so a shared manifest keeps its other tags. Blobs stay until a garbage collection.
func registryDeleteScript(ref string) string {
	repo, tag, _ := strings.Cut(ref, ":")
	return "curl -sf -X DELETE " + registryURL + "/v2/" + repo + "/manifests/" + tag
}
