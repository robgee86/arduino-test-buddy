// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package host

import (
	"context"
	"fmt"
	"os"
)

// PruneRequest names what to give back: one tag's images, the whole build cache, or everything the tool created.
type PruneRequest struct {
	Tag   string `json:"tag,omitempty" jsonschema:"delete this tag from the registry and free the layers no other tag uses"`
	Cache bool   `json:"cache,omitempty" jsonschema:"empty the tool's build cache; the next push builds from scratch"`
	All   bool   `json:"all,omitempty" jsonschema:"remove the registry container, its folder and the builder with its cache"`
}

// PruneResult lists each step and how much the registry folder shrank.
type PruneResult struct {
	Steps   []Step `json:"steps"`
	FreedMB int64  `json:"freed_mb"`
	Path    string `json:"path"`
	SizeMB  int64  `json:"size_mb"`
}

// Prune waits for running pushes to end, then removes what was asked; it touches only the tool's registry, folder and builder.
func (h *Host) Prune(ctx context.Context, req PruneRequest) (*PruneResult, error) {
	if req.Tag == "" && !req.Cache && !req.All {
		return nil, fmt.Errorf("nothing to prune: give a tag, the cache or all")
	}
	exclusive, err := h.lock(true, true)
	if err != nil {
		return nil, err
	}

	res := &PruneResult{Steps: []Step{}, Path: h.RegistryDir()}
	before := dirSizeMB(h.RegistryDir())
	if req.All {
		res.Steps = append(res.Steps, h.removeAll(ctx)...)
		exclusive.release()
		// The lock file lives in the folder, so the folder goes last, once the lock is released.
		folder := Step{Name: "remove " + h.Dir}
		if err := os.RemoveAll(h.Dir); err != nil {
			folder.Output, folder.ExitCode = err.Error(), 1
		}
		res.Steps = append(res.Steps, folder)
		res.FreedMB = before
		return res, nil
	}
	defer exclusive.release()
	if req.Tag != "" {
		steps, err := h.pruneTag(ctx, req.Tag)
		if err != nil {
			return nil, err
		}
		res.Steps = append(res.Steps, steps...)
	}
	if req.Cache {
		step, _ := h.step(ctx, "empty build cache", "", nil, "docker", "buildx", "prune", "--builder", BuilderName, "--force", "--all")
		res.Steps = append(res.Steps, step)
	}
	res.SizeMB = dirSizeMB(h.RegistryDir())
	res.FreedMB = before - res.SizeMB
	return res, nil
}

// pruneTag deletes the tag from every repository, then collects the layers nothing references any more.
func (h *Host) pruneTag(ctx context.Context, tag string) ([]Step, error) {
	if _, err := h.EnsureRegistry(ctx); err != nil {
		return nil, err
	}
	refs, err := h.Images(ctx, tag)
	if err != nil {
		return nil, err
	}
	var steps []Step
	for _, ref := range refs {
		step := Step{Name: "delete " + ref}
		if err := h.deleteTag(ctx, ref); err != nil {
			step.Output, step.ExitCode = err.Error(), 1
		}
		steps = append(steps, step)
	}
	gc, _ := h.step(ctx, "collect unreferenced layers", "", nil, "docker", "exec", RegistryName,
		"registry", "garbage-collect", "--delete-untagged", "/etc/distribution/config.yml")
	// The registry caches blob descriptors in memory and would keep claiming the deleted layers exist, so later pushes would skip them.
	restart, _ := h.step(ctx, "restart registry to drop its cache", "", nil, "docker", "restart", RegistryName)
	if err := h.waitReady(ctx); err != nil {
		return nil, err
	}
	return append(steps, gc, restart), nil
}

// removeAll removes the registry container and the builder; Prune removes the folder after releasing its lock.
func (h *Host) removeAll(ctx context.Context) []Step {
	container, _ := h.step(ctx, "remove registry container", "", nil, "docker", "rm", "-f", RegistryName)
	steps := []Step{container}
	if _, code := h.cmd.Run(ctx, "", nil, "docker", "buildx", "inspect", BuilderName); code == 0 {
		builder, _ := h.step(ctx, "remove builder and its cache", "", nil, "docker", "buildx", "rm", BuilderName)
		steps = append(steps, builder)
	}
	return steps
}
