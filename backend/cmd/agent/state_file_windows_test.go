package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsStateWritePreservesLockedPreviousSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	const old = `{"version":1,"events":[]}`
	if err := os.WriteFile(path, []byte(old), 0600); err != nil {
		t.Fatal(err)
	}
	held, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := atomicWriteFile(path, []byte(`{"version":1,"presence":[]}`), 0600); err == nil {
		t.Fatal("expected sharing violation")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != old {
		t.Fatalf("old snapshot damaged: %s %v", got, err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatalf("temporary state remains: %v", err)
	}
	held.Close()
	if err := atomicWriteFile(path, []byte(`{"version":1,"presence":[]}`), 0600); err != nil {
		t.Fatal("retry after unlock", err)
	}
}
