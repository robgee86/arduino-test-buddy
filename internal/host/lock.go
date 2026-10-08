// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

//go:build unix

package host

import (
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

// lock is an flock on the cache folder's lock file: pushes share it, pruning takes it alone. The kernel releases it when the process dies, so it can never go stale.
type lock struct {
	f *os.File
}

// errBusy reports that a non-waiting lock found another holder.
var errBusy = errors.New("another push or prune holds the cache")

func (h *Host) lock(exclusive, wait bool) (*lock, error) {
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(filepath.Join(h.Dir, "lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	how := syscall.LOCK_SH
	if exclusive {
		how = syscall.LOCK_EX
	}
	if !wait {
		how |= syscall.LOCK_NB
	}
	if err := syscall.Flock(int(f.Fd()), how); err != nil {
		f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errBusy
		}
		return nil, err
	}
	return &lock{f: f}, nil
}

func (l *lock) release() {
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	l.f.Close()
}
