// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import "testing"

func TestTheTunnelBindsTheBoardsLoopbackOnly(t *testing.T) {
	if got := remoteForward(23456, "54321"); got != "127.0.0.1:23456:127.0.0.1:54321" {
		t.Errorf("remote forward %q must bind 127.0.0.1 on the board, whatever its sshd's GatewayPorts", got)
	}
}
