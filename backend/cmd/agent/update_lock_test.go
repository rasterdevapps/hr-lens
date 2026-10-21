package main

import (
	"path/filepath"
	"testing"
)

func TestUpdateWriterLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent")
	unlock, err := acquireUpdateLock(path)
	if err != nil {
		t.Fatal(err)
	}
	second, err := acquireUpdateLock(path)
	if err == nil {
		second()
		unlock()
		t.Fatal("two update writers acquired the same installation")
	}
	unlock()
	unlock, err = acquireUpdateLock(path)
	if err != nil {
		t.Fatal("lock not released", err)
	}
	unlock()
}
