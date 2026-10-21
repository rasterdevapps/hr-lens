package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecoverOnlyInactiveSessionSpools(t *testing.T) {
	root := t.TempDir()
	active := filepath.Join(root, "session-1")
	old := filepath.Join(root, "session-2")
	current := filepath.Join(root, "session-3")
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	for _, dir := range []string{active, old, current} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	release, err := ownSpool(active)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	for _, dir := range []string{active, old} {
		q := &agent{spool: dir, log: log, events: []event{{App: "synthetic", StartedAt: time.Now(), Seconds: 3}}}
		if err := q.checkpointSnapshot(); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "recording.part"), []byte("incomplete"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	a := &agent{cfg: config{Server: srv.URL, Token: "test"}, http: srv.Client(), log: log, spool: current}
	if err := a.drainAbandonedSpools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("sent %d batches, want abandoned only", calls.Load())
	}
	if _, err := os.Stat(filepath.Join(active, "recording.part")); err != nil {
		t.Fatal("touched active recorder", err)
	}
	if _, err := os.Stat(filepath.Join(old, "recording.part")); !os.IsNotExist(err) {
		t.Fatal("did not remove abandoned partial")
	}
	if err := a.drainAbandonedSpools(context.Background()); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatal("acknowledged abandoned batch uploaded again")
	}
}
