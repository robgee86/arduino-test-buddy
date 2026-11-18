// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package plugin

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

// fakeProgram stands for the released program: it prints its arguments, so a test sees it ran and with what.
const fakeProgram = `package main

import (
	"fmt"
	"os"
	"strings"
)

func main() { fmt.Println("fake buddy: " + strings.Join(os.Args[1:], "|")) }
`

// release is a fake GitHub release of the plugin's version for this machine: the archive the launcher asks for and its checksums file.
type release struct {
	url      string
	requests atomic.Int32
	// tamper makes the checksums file disagree with the archive.
	tamper atomic.Bool
}

func exeName() string {
	if runtime.GOOS == "windows" {
		return "arduino-test-buddy.exe"
	}
	return "arduino-test-buddy"
}

// archive packs the program as the release does: a zip on Windows, a gzipped tar elsewhere.
func archive(t *testing.T, program []byte) (name string, data []byte) {
	var manifest struct{ Version string }
	read(t, ".claude-plugin/plugin.json", &manifest)
	base := fmt.Sprintf("arduino-test-buddy_%s_%s_%s", manifest.Version, runtime.GOOS, runtime.GOARCH)
	var buf bytes.Buffer
	if runtime.GOOS == "windows" {
		zw := zip.NewWriter(&buf)
		w, err := zw.Create(exeName())
		if err != nil {
			t.Fatal(err)
		}
		w.Write(program)
		zw.Close()
		return base + ".zip", buf.Bytes()
	}
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "LICENSE", Mode: 0o644, Size: 3})
	tw.Write([]byte("GPL"))
	tw.WriteHeader(&tar.Header{Name: exeName(), Mode: 0o755, Size: int64(len(program))})
	tw.Write(program)
	tw.Close()
	gz.Close()
	return base + ".tar.gz", buf.Bytes()
}

func newRelease(t *testing.T) *release {
	src := t.TempDir()
	for file, content := range map[string]string{"main.go": fakeProgram, "go.mod": "module fakebuddy\n\ngo 1.24\n"} {
		if err := os.WriteFile(filepath.Join(src, file), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	built := filepath.Join(src, exeName())
	build := exec.Command("go", "build", "-o", built, ".")
	build.Dir = src
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the fake program: %v\n%s", err, out)
	}
	program, err := os.ReadFile(built)
	if err != nil {
		t.Fatal(err)
	}
	name, data := archive(t, program)
	sum := sha256.Sum256(data)
	var manifest struct{ Version string }
	read(t, ".claude-plugin/plugin.json", &manifest)
	r := &release{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.requests.Add(1)
		switch req.URL.Path {
		case "/v" + manifest.Version + "/" + name:
			w.Write(data)
		case "/v" + manifest.Version + "/checksums.txt":
			digest := hex.EncodeToString(sum[:])
			if r.tamper.Load() {
				digest = strings.Repeat("0", 64)
			}
			fmt.Fprintf(w, "%s  some-other-file.tar.gz\n%s  %s\n", strings.Repeat("1", 64), digest, name)
		default:
			http.NotFound(w, req)
		}
	}))
	t.Cleanup(srv.Close)
	r.url = srv.URL
	return r
}

// launch runs the launcher as Claude Code does, with home as the plugin's data folder.
func (r *release) launch(home string, args ...string) (stdout, stderr string, err error) {
	script, _ := filepath.Abs(filepath.Join(root, launcher))
	cmd := exec.Command("sh", append([]string{script}, args...)...)
	cmd.Env = append(os.Environ(), "ARDUINO_TEST_BUDDY_HOME="+home, "ARDUINO_TEST_BUDDY_RELEASES="+r.url)
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err = cmd.Run()
	return out.String(), errOut.String(), err
}

func TestTheLauncherFetchesTheProgramOnceAndRunsItWithItsArguments(t *testing.T) {
	r, home := newRelease(t), t.TempDir()
	for run := 1; run <= 2; run++ {
		stdout, stderr, err := r.launch(home, "mcp", "two words")
		// Stdout carries the MCP protocol: only the program may write there
		if err != nil || strings.TrimSpace(stdout) != "fake buddy: mcp|two words" {
			t.Fatalf("run %d: the program must run with the launcher's arguments and own stdout: %q %v\n%s", run, stdout, err, stderr)
		}
	}
	if got := r.requests.Load(); got != 2 {
		t.Errorf("the archive and its checksums are fetched once, 2 requests in all, not %d", got)
	}
}

func TestTheLauncherRefusesAnArchiveThatDoesNotMatchItsChecksum(t *testing.T) {
	r, home := newRelease(t), t.TempDir()
	r.tamper.Store(true)
	stdout, stderr, err := r.launch(home, "mcp")
	if err == nil || stdout != "" || !strings.Contains(stderr, "does not match its checksum") {
		t.Errorf("a changed download must fail, say why on stderr and print nothing on stdout: %q %q %v", stdout, stderr, err)
	}
	left, _ := filepath.Glob(filepath.Join(home, "*", exeName()))
	if len(left) != 0 {
		t.Errorf("no program may be kept from a refused download: %v", left)
	}
	r.tamper.Store(false)
	if stdout, stderr, err := r.launch(home, "mcp"); err != nil || strings.TrimSpace(stdout) != "fake buddy: mcp" {
		t.Errorf("the next start must fetch again and run: %q %v\n%s", stdout, err, stderr)
	}
}

func TestTheLauncherDropsTheVersionsAnUpdateLeftBehind(t *testing.T) {
	r, home := newRelease(t), t.TempDir()
	old := filepath.Join(home, "0.0.1")
	if err := os.MkdirAll(old, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, exeName()), []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if _, stderr, err := r.launch(home, "mcp"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	if _, err := os.Stat(old); err == nil {
		t.Error("the version an update left behind must go, or the data folder grows with every release")
	}
}

func TestTheLauncherTouchesNothingButOlderVersions(t *testing.T) {
	r, home := newRelease(t), t.TempDir()
	// What is not a version folder: another session's fetch in progress, and anything a user or a tool put there
	kept := []string{filepath.Join("fetch.Ab12Cd", "out", exeName()), filepath.Join("notes", "keep.txt"), "loose.txt"}
	for _, path := range kept {
		full := filepath.Join(home, path)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, stderr, err := r.launch(home, "mcp"); err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	for _, path := range kept {
		if _, err := os.Stat(filepath.Join(home, path)); err != nil {
			t.Errorf("the launcher removed %s, which is not an older version", path)
		}
	}
}

func TestSessionsStartingTogetherOnAFreshInstallAllGetTheProgram(t *testing.T) {
	r, home := newRelease(t), t.TempDir()
	const sessions = 4
	type result struct {
		stdout, stderr string
		err            error
	}
	results := make(chan result, sessions)
	for range sessions {
		go func() {
			stdout, stderr, err := r.launch(home, "mcp")
			results <- result{stdout, stderr, err}
		}()
	}
	for range sessions {
		// A server that fails once is skipped by the sessions that follow, so no start may lose to another one's fetch
		if got := <-results; got.err != nil || strings.TrimSpace(got.stdout) != "fake buddy: mcp" {
			t.Errorf("every session must end up running the program: %q %v\n%s", got.stdout, got.err, got.stderr)
		}
	}
	left, _ := filepath.Glob(filepath.Join(home, "fetch.*"))
	if len(left) != 0 {
		t.Errorf("no fetch folder may stay behind: %v", left)
	}
}
