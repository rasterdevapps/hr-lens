//go:build windows

package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestWindowsUploadedFileLockedByAnotherProcess(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synthetic.jpg")
	if err := os.WriteFile(path, []byte("synthetic image"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeMeta(path, map[string]string{"takenAt": "2026-10-21T10:00:00Z"})
	locks := make(chan *os.File, 1)
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		io.Copy(io.Discard, r.Body)
		f, err := os.Open(path)
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		locks <- f
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()
	a := testAgent(server.URL)
	if err := a.upload(context.Background(), path); err == nil {
		t.Fatal("expected checked Windows sharing violation")
	}
	var lock *os.File
	select {
	case lock = <-locks:
	default:
		t.Fatal("server did not hold a file handle")
	}
	defer lock.Close()
	if exists, err := hasUploadedReceipt(path); err != nil || !exists {
		t.Fatalf("missing acceptance receipt: %v", err)
	}
	if _, err := os.Stat(path + ".json"); err != nil {
		t.Fatal("metadata lost before cleanup", err)
	}
	lock.Close()
	// A fresh agent after restart must perform cleanup only.
	if err := testAgent(server.URL).upload(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 1 {
		t.Fatal("re-uploaded an accepted file")
	}
}
