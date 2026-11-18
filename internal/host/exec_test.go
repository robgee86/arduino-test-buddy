// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package host

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// The test program doubles as a command that starts a child and waits for it, as task does with docker: the child inherits the output and sleeps.
const (
	helperRole  = "BUDDY_TEST_EXEC_ROLE"
	helperReady = "BUDDY_TEST_EXEC_READY"
)

func TestMain(m *testing.M) {
	switch os.Getenv(helperRole) {
	case "command":
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), helperRole+"=child")
		child.Stdout, child.Stderr = os.Stdout, os.Stderr
		if err := child.Start(); err != nil {
			os.Exit(1)
		}
		os.WriteFile(os.Getenv(helperReady), []byte(strconv.Itoa(child.Process.Pid)), 0o644)
		child.Wait()
		os.Exit(0)
	case "child":
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func running(pid int) bool {
	if runtime.GOOS == "windows" {
		out, _ := exec.Command("tasklist", "/FI", fmt.Sprintf("PID eq %d", pid), "/NH").Output()
		return strings.Contains(string(out), strconv.Itoa(pid))
	}
	return exec.Command("kill", "-0", strconv.Itoa(pid)).Run() == nil
}

func TestACancelledCommandReturnsAtOnceAndTakesWhatItStartedWithIt(t *testing.T) {
	ready := filepath.Join(t.TempDir(), "ready")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	returned := make(chan struct{})
	go func() {
		Exec{}.Run(ctx, "", []string{helperRole + "=command", helperReady + "=" + ready}, os.Args[0])
		close(returned)
	}()

	var child int
	for deadline := time.Now().Add(10 * time.Second); child == 0; time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the command never started its child")
		}
		if pid, err := os.ReadFile(ready); err == nil {
			child, _ = strconv.Atoi(string(pid))
		}
	}
	t.Cleanup(func() {
		if p, err := os.FindProcess(child); err == nil {
			p.Kill()
		}
	})

	cancel()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the call still waits for the output its command's child keeps open")
	}
	// Windows has no process group to end: there the child runs on, and only the call returns
	if runtime.GOOS == "windows" {
		return
	}
	for deadline := time.Now().Add(2 * time.Second); running(child); time.Sleep(20 * time.Millisecond) {
		if time.Now().After(deadline) {
			t.Fatal("the child of the cancelled command still runs")
		}
	}
}
