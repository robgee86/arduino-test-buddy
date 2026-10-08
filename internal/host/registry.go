// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"slices"
	"strconv"
	"strings"
	"time"
)

// RegistryStatus is where the registry keeps its data, how big it is and what it holds.
type RegistryStatus struct {
	URL     string   `json:"url"`
	Created bool     `json:"created"`
	Path    string   `json:"path"`
	SizeMB  int64    `json:"size_mb"`
	Images  []string `json:"images"`
}

// EnsureRegistry starts the registry container once, then only reports; its data is a plain folder owned by the user.
func (h *Host) EnsureRegistry(ctx context.Context) (*RegistryStatus, error) {
	status := &RegistryStatus{URL: h.url, Path: h.RegistryDir()}
	state, _ := h.cmd.Run(ctx, "", nil, "docker", "ps", "-a", "--filter", "name=^"+RegistryName+"$", "--format", "{{.State}}")
	switch strings.TrimSpace(state) {
	case "running":
	case "":
		if err := os.MkdirAll(h.RegistryDir(), 0o755); err != nil {
			return nil, err
		}
		// Running as the user keeps every file in the folder deletable without root.
		user := strconv.Itoa(os.Getuid()) + ":" + strconv.Itoa(os.Getgid())
		out, code := h.cmd.Run(ctx, "", nil, "docker", "run", "-d", "--name", RegistryName, "--restart", "unless-stopped",
			"--user", user, "-p", "127.0.0.1:"+RegistryPort+":5000", "-v", h.RegistryDir()+":/var/lib/registry",
			"-e", "REGISTRY_STORAGE_DELETE_ENABLED=true", RegistryImage)
		if code != 0 {
			return nil, fmt.Errorf("could not start the registry: %s", strings.TrimSpace(out))
		}
		status.Created = true
	default:
		if out, code := h.cmd.Run(ctx, "", nil, "docker", "start", RegistryName); code != 0 {
			return nil, fmt.Errorf("could not restart the registry: %s", strings.TrimSpace(out))
		}
	}
	if err := h.waitReady(ctx); err != nil {
		return nil, err
	}
	images, err := h.Images(ctx, "")
	if err != nil {
		return nil, err
	}
	status.Images = images
	status.SizeMB = dirSizeMB(h.RegistryDir())
	return status, nil
}

func (h *Host) waitReady(ctx context.Context) error {
	for range 30 {
		if resp, err := h.request(ctx, http.MethodGet, "/v2/"); err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("the registry does not answer on %s", h.url)
}

// Images lists the registry's repository:tag entries, only those carrying tag when it is given.
func (h *Host) Images(ctx context.Context, tag string) ([]string, error) {
	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	if err := h.getJSON(ctx, "/v2/_catalog", &catalog); err != nil {
		return nil, err
	}
	refs := []string{}
	for _, repo := range catalog.Repositories {
		var list struct {
			Tags []string `json:"tags"`
		}
		if err := h.getJSON(ctx, "/v2/"+repo+"/tags/list", &list); err != nil {
			return nil, err
		}
		for _, t := range list.Tags {
			if tag == "" || t == tag {
				refs = append(refs, repo+":"+t)
			}
		}
	}
	slices.Sort(refs)
	return refs, nil
}

// deleteTag removes one repository:tag; the registry deletes by tag, so a manifest shared with another tag keeps that one.
func (h *Host) deleteTag(ctx context.Context, ref string) error {
	repo, tag, _ := strings.Cut(ref, ":")
	resp, err := h.request(ctx, http.MethodDelete, "/v2/"+repo+"/manifests/"+tag)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusAccepted {
		return fmt.Errorf("delete %s: HTTP %d", ref, resp.StatusCode)
	}
	return nil
}

func (h *Host) getJSON(ctx context.Context, path string, v any) error {
	resp, err := h.request(ctx, http.MethodGet, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("registry %s: HTTP %d", path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (h *Host) request(ctx context.Context, method, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, h.url+path, nil)
	if err != nil {
		return nil, err
	}
	return http.DefaultClient.Do(req)
}
