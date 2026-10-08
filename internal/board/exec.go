// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"context"

	"github.com/robgee86/arduino-test-buddy/internal/shell"
)

// Exec runs a shell command inside a container of the board.
func (b *Board) Exec(ctx context.Context, container, command string) (Output, error) {
	return b.run(ctx, shell.Join("docker", "exec", container, "sh", "-c", command))
}

// Shell runs a shell command on the board's host OS; App CLI calls go through the other operations so they stay serialized.
func (b *Board) Shell(ctx context.Context, command string) (Output, error) {
	return b.run(ctx, command)
}
