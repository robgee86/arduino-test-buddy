// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package board

import (
	"os"
	"path/filepath"
)

// mkAppDir creates the smallest valid app folder.
func mkAppDir(dir string) error {
	if err := os.MkdirAll(filepath.Join(dir, "python"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "app.yaml"), []byte("name: test\nbricks: []\n"), 0o644); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "python", "main.py"), []byte("print('hi')\n"), 0o644)
}
