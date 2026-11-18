// SPDX-FileCopyrightText: Arduino s.r.l. and/or its affiliated companies
// SPDX-License-Identifier: GPL-3.0-or-later

package host

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

// allBytes locks the whole file, whatever its size.
const allBytes = ^uint32(0)

// lockFile takes a LockFileEx lock on f, shared or exclusive; without wait a held lock is errBusy. Like flock it belongs to the open handle, so two handles of one process exclude each other too.
func lockFile(f *os.File, exclusive, wait bool) error {
	var flags uint32
	if exclusive {
		flags |= windows.LOCKFILE_EXCLUSIVE_LOCK
	}
	if !wait {
		flags |= windows.LOCKFILE_FAIL_IMMEDIATELY
	}
	err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, allBytes, allBytes, new(windows.Overlapped))
	if errors.Is(err, windows.ERROR_LOCK_VIOLATION) {
		return errBusy
	}
	return err
}

func unlockFile(f *os.File) {
	_ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, allBytes, allBytes, new(windows.Overlapped))
}
