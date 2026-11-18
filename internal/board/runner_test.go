// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestTheTunnelBindsTheBoardsLoopbackOnly(t *testing.T) {
	if got := remoteForward(23456, "54321"); got != "127.0.0.1:23456:127.0.0.1:54321" {
		t.Errorf("remote forward %q must bind 127.0.0.1 on the board, whatever its sshd's GatewayPorts", got)
	}
}

func TestABoardNameSSHWouldReadAsAnOptionIsRefused(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// ssh runs a ProxyCommand on this machine before it connects anywhere
	ran := filepath.Join(t.TempDir(), "ran")
	for _, target := range []string{"-oProxyCommand=touch " + ran, ""} {
		s := SSH{Target: target}
		if _, err := s.Run(ctx, "x"); err == nil {
			t.Errorf("a script must not run on the board %q", target)
		}
		if err := s.Push(ctx, t.TempDir(), "/tmp/x"); err == nil {
			t.Errorf("a folder must not be pushed to the board %q", target)
		}
		if lease, err := s.Lease(ctx, "", time.Second); err == nil {
			lease.Close()
			t.Errorf("a turn must not be taken on the board %q", target)
		}
	}
	if _, err := os.Stat(ran); err == nil {
		t.Error("the board name ran a program on this machine")
	}
}
