// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package host

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Config is what a developer sets once for every session, in ~/.config/arduino-test-buddy/config.yaml on every platform, next to the boards file the skill reads.
type Config struct {
	// Source is the app-bricks-py checkout a push builds when none is given and the current folder is not one; a leading ~ is the home folder.
	Source string `yaml:"source"`
}

// bakeFile is what tells an app-bricks-py checkout from any other folder.
const bakeFile = "docker-bake.hcl"

// configFile is the developer's config file, the same path on every platform so the docs name one.
func configFile() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "arduino-test-buddy", "config.yaml")
}

// config reads the developer's config; a missing file is an empty config, an unknown key is an error, so a typo never passes for a setting.
func (h *Host) config() (Config, error) {
	var cfg Config
	if h.ConfigFile == "" {
		return cfg, nil
	}
	f, err := os.Open(h.ConfigFile)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return cfg, err
	}
	defer f.Close()
	dec := yaml.NewDecoder(f)
	dec.KnownFields(true)
	// An empty file has no document to decode: no setting, no error.
	if err := dec.Decode(&cfg); err != nil && !errors.Is(err, io.EOF) {
		return cfg, fmt.Errorf("%s: %w", h.ConfigFile, err)
	}
	if rest, ok := strings.CutPrefix(cfg.Source, "~"); ok && (rest == "" || rest[0] == '/' || rest[0] == '\\') {
		home, err := os.UserHomeDir()
		if err != nil {
			return cfg, err
		}
		cfg.Source = filepath.Join(home, rest)
	}
	return cfg, nil
}

// source picks the checkout a push builds: the one asked for, else the current folder when it is a checkout, else the one of the config file. The current folder comes before the config so a session inside a worktree builds that worktree.
func (h *Host) source(asked string) (string, error) {
	if asked != "" {
		dir, err := filepath.Abs(asked)
		if err != nil {
			return "", err
		}
		if !isCheckout(dir) {
			return "", fmt.Errorf("%s is not an app-bricks-py checkout: no %s", dir, bakeFile)
		}
		return dir, nil
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	if isCheckout(cwd) {
		return cwd, nil
	}
	cfg, err := h.config()
	if err != nil {
		return "", err
	}
	if cfg.Source == "" {
		return "", fmt.Errorf("%s is not an app-bricks-py checkout: give the source, run from a checkout, or set `source` in %s", cwd, h.ConfigFile)
	}
	if !isCheckout(cfg.Source) {
		return "", fmt.Errorf("%s, the source set in %s, is not an app-bricks-py checkout: no %s", cfg.Source, h.ConfigFile, bakeFile)
	}
	return cfg.Source, nil
}

func isCheckout(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, bakeFile))
	return err == nil
}
