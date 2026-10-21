//go:build linux

package main

import (
	"os"
	"syscall"
)

func acquireUpdateLock(bin string) (func(), error) {
	f, err := os.OpenFile(bin+".update-lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, err
	}
	return func() { f.Close() }, nil
}
