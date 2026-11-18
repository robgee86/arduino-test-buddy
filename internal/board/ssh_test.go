// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/robgee86/arduino-test-buddy/internal/shell"
)

// sshTargetEnv names a Linux machine reachable over ssh without a prompt, with flock, tar and curl: a board, or the sshd CI starts.
// The tests below drive it with the real ssh client of this machine, which no fake can stand for; without the variable they are skipped, with it an unreachable target fails them.
const sshTargetEnv = "BUDDY_TEST_SSH_TARGET"

func sshTarget(t *testing.T) (SSH, context.Context) {
	target := os.Getenv(sshTargetEnv)
	if target == "" {
		t.Skipf("set %s to run against a real ssh target", sshTargetEnv)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return SSH{Target: target}, ctx
}

func TestSSHDeliversTheScriptsOfTheToolIntact(t *testing.T) {
	s, ctx := sshTarget(t)
	// Every character the tool's scripts carry through the local process launch, ssh and the remote shell
	words := []string{"it's", `a "b"`, "$HOME", "x && y; z", `back\slash`, "{{.Names}}|{{.State}}", "50%", ""}
	out, err := s.Run(ctx, sectioned(map[string]string{
		"words":  shell.Join(append([]string{"printf", `%s\n`}, words...)...),
		"format": "printf '%s\\n' '{{.Names}}|{{.Image}}|{{.Status}}'",
		"gone":   "ls /no/such/folder",
	}, []string{"words", "format", "gone"}))
	if err != nil {
		t.Fatal(err)
	}
	sections := parseSections(out.Stdout)
	if got := strings.Split(sections["words"], "\n"); strings.Join(got, "\x00") != strings.Join(words[:len(words)-1], "\x00") {
		t.Errorf("the words reached the board as %q, want %q", got, words)
	}
	if sections["format"] != "{{.Names}}|{{.Image}}|{{.Status}}" || sections["gone"] != "" || !out.OK() {
		t.Errorf("unexpected sections: %q", sections)
	}

	out, err = s.Run(ctx, "echo to-stdout; echo to-stderr >&2; exit 3")
	if err != nil || out.ExitCode != 3 || strings.TrimSpace(out.Stdout) != "to-stdout" || strings.TrimSpace(out.Stderr) != "to-stderr" {
		t.Errorf("a failing command is a result, not an error, with both streams apart: %+v %v", out, err)
	}
}

func TestSSHPushReplacesTheRemoteFolderWithTheLocalOne(t *testing.T) {
	s, ctx := sshTarget(t)
	local := t.TempDir()
	if err := mkAppDir(local); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "python", "with space.py"), []byte("x = 'è'\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	remote := fmt.Sprintf("/tmp/bt-ssh-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() { _, _ = s.Run(context.Background(), shell.Join("rm", "-rf", remote)) })
	if out, err := s.Run(ctx, shell.Join("mkdir", "-p", remote)+" && "+shell.Join("touch", remote+"/stale")); err != nil || !out.OK() {
		t.Fatalf("preparing the remote folder: %+v %v", out, err)
	}
	if err := s.Push(ctx, local, remote); err != nil {
		t.Fatal(err)
	}
	out, err := s.Run(ctx, "cd "+shell.Quote(remote)+" && find . -type f | sort && cat app.yaml 'python/with space.py'")
	if err != nil {
		t.Fatal(err)
	}
	want := "./app.yaml\n./python/main.py\n./python/with space.py\nname: test\nbricks: []\nx = 'è'\n"
	if out.Stdout != want {
		t.Errorf("the remote folder must hold the local files and nothing else:\n got %q\nwant %q", out.Stdout, want)
	}
}

func TestSSHTurnsQueueOnTheBoard(t *testing.T) {
	s, ctx := sshTarget(t)
	first, err := s.Lease(ctx, "", time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := s.Lease(ctx, "", time.Second); err == nil {
		second.Close()
		first.Close()
		t.Fatal("a second session got the board while the first one held it")
	} else if !strings.Contains(err.Error(), "not free") {
		t.Errorf("a busy board must say so: %v", err)
	}
	first.Close()
	next, err := s.Lease(ctx, "", 30*time.Second)
	if err != nil {
		t.Fatalf("the board must be free once the turn ended: %v", err)
	}
	next.Close()
}

func TestSSHTunnelLeadsToTheHostPortOnlyForTheTurn(t *testing.T) {
	s, ctx := sshTarget(t)
	var hits atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		fmt.Fprint(w, "from the developer machine")
	}))
	defer host.Close()
	u, _ := url.Parse(host.URL)
	lease, err := s.Lease(ctx, u.Port(), 30*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	fetch := fmt.Sprintf("curl -s --max-time 10 http://127.0.0.1:%d/v2/", lease.Port)
	out, err := s.Run(ctx, fetch)
	if err != nil || out.Stdout != "from the developer machine" || hits.Load() != 1 {
		lease.Close()
		t.Fatalf("the board must reach the host port through the tunnel: %+v %v, %d requests", out, err, hits.Load())
	}
	lease.Close()
	out, err = s.Run(ctx, fetch)
	if err != nil || out.OK() || hits.Load() != 1 {
		t.Errorf("the tunnel must close with the turn: %+v %v, %d requests", out, err, hits.Load())
	}
}
