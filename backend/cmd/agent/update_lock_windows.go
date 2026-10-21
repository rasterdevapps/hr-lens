//go:build windows

package main

import (
	"golang.org/x/sys/windows"
	"os"
)

// A byte-range lock is shared across Windows sessions using the same install.
// Closing the handle (including on a crash) releases it; a stale lock file is harmless.
func acquireUpdateLock(bin string) (func(), error) {
	f, err := os.OpenFile(bin+".update-lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	var ov windows.Overlapped
	err = windows.LockFileEx(windows.Handle(f.Fd()), windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &ov)
	if err != nil {
		f.Close()
		return nil, err
	}
	return func() { windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ov); f.Close() }, nil
}
