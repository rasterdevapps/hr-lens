package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSnapshotRejectsUnknownVersionAndTrailingData(t *testing.T) {
	p := filepath.Join(t.TempDir(), "pending-state.json")
	for _, data := range []string{`{"version":2}`, `{"version":1} {"version":1}`} {
		if err := os.WriteFile(p, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := readSnapshotFile(p); err == nil {
			t.Fatal("accepted invalid snapshot", data)
		}
	}
}

func TestSpoolEvictsChronologicallyAcrossMediaTypes(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "screenshot-old.jpg")
	newer := filepath.Join(dir, "recording-new.webm")
	for _, p := range []string{older, newer} {
		if err := os.WriteFile(p, []byte("12345678"), 0600); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now()
	if err := os.Chtimes(older, now.Add(-time.Hour), now.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := enforceSpoolCap(dir, 8); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(older); !os.IsNotExist(err) {
		t.Fatal("older screenshot retained")
	}
	if _, err := os.Stat(newer); err != nil {
		t.Fatal("newer recording evicted", err)
	}
}

func TestWTSPreservesRapidLockTransitions(t *testing.T) {
	var st sessionLifecycleState
	st.initFromWTS(parsedWTSSession{SessionID: 1, LockKnown: true})
	st.applyWTSSessionChange(wtsSessionLock)
	st.applyWTSSessionChange(wtsSessionUnlock)
	events := st.takePresenceTransitions()
	if len(events) != 2 || events[0].Kind != "lock" || events[1].Kind != "unlock" {
		t.Fatalf("lost rapid transitions: %+v", events)
	}
	if events[1].At.Before(events[0].At) {
		t.Fatal("transitions out of order")
	}
	if len(st.takePresenceTransitions()) != 0 {
		t.Fatal("repeated transitions")
	}
	st.captureSuspended(true)
	if len(st.takePresenceTransitions()) != 0 {
		t.Fatal("UAC generated attendance")
	}
	if st.signedOut {
		t.Fatal("ordinary session state became sign-out")
	}
	st.applyWTSSessionChange(wtsSessionLogoff)
	if !st.signedOut {
		t.Fatal("logoff not recorded")
	}
}
