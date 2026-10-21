package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
	"unicode/utf16"
	"unsafe"
)

func TestSnapshotRoundtripAndLoginDedup(t *testing.T) {
	dir := t.TempDir()
	spool := sessionSpoolDir(dir, "session-42")
	if err := os.MkdirAll(spool, 0o700); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}

	baseTime := time.Date(2026, 10, 21, 9, 0, 0, 0, time.UTC)
	min1 := baseTime.Truncate(time.Minute)
	min2 := min1.Add(time.Minute)

	a1 := &agent{
		spool: spool,
		input: map[time.Time]*inputMinute{
			min1: {Minute: min1, Keys: 45, Clicks: 12},
			min2: {Minute: min2, Keys: 18, Clicks: 4},
		},
		events: []event{
			{StartedAt: baseTime, Seconds: 30, App: "code", AppName: "Visual Studio Code", Title: "main.go"},
		},
		open: &event{
			StartedAt: baseTime.Add(30 * time.Second),
			Seconds:   15,
			App:       "chrome",
			AppName:   "Google Chrome",
			Title:     "Docs",
			Domain:    "docs.example.com",
		},
	}
	a1.noteLogin(baseTime, true)
	a1.note("lock", baseTime.Add(5*time.Minute))

	if err := a1.checkpointSnapshot(); err != nil {
		t.Fatalf("checkpointSnapshot: %v", err)
	}

	// Recover into a fresh agent before noting login for the same session.
	a2 := &agent{
		spool: spool,
		input: map[time.Time]*inputMinute{},
	}
	if err := a2.loadSnapshot(); err != nil {
		t.Fatalf("loadSnapshot: %v", err)
	}
	// Noting the same session start after recovery must deduplicate the login presence event.
	a2.noteLogin(baseTime, true)

	if len(a2.presence) != 2 {
		t.Fatalf("recovered presence count = %d, want 2: %+v", len(a2.presence), a2.presence)
	}
	if a2.presence[0].Kind != "login" || !a2.presence[0].At.Equal(baseTime) {
		t.Fatalf("first presence = %+v, want login at %v", a2.presence[0], baseTime)
	}
	if a2.presence[1].Kind != "lock" {
		t.Fatalf("second presence = %+v, want lock", a2.presence[1])
	}

	// Both the closed event and the checkpointed in-progress open event should be recovered.
	if len(a2.events) != 2 {
		t.Fatalf("recovered events count = %d, want 2: %+v", len(a2.events), a2.events)
	}
	if a2.events[0].App != "code" || a2.events[0].Seconds != 30 {
		t.Fatalf("first event = %+v", a2.events[0])
	}
	if a2.events[1].App != "chrome" || a2.events[1].Seconds != 15 || a2.events[1].Domain != "docs.example.com" {
		t.Fatalf("second (recovered open) event = %+v", a2.events[1])
	}

	if len(a2.input) != 2 {
		t.Fatalf("recovered input minutes = %d, want 2", len(a2.input))
	}
	if got := a2.input[min1]; got == nil || got.Keys != 45 || got.Clicks != 12 {
		t.Fatalf("min1 = %+v, want 45 keys / 12 clicks", got)
	}
	if got := a2.input[min2]; got == nil || got.Keys != 18 || got.Clicks != 4 {
		t.Fatalf("min2 = %+v, want 18 keys / 4 clicks", got)
	}
}

func TestSnapshotBoundsEnforcement(t *testing.T) {
	spool := t.TempDir()
	base := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)

	a := &agent{
		spool: spool,
		input: map[time.Time]*inputMinute{},
	}
	for i := 0; i < maxPendingEvents+250; i++ {
		a.events = append(a.events, event{
			StartedAt: base.Add(time.Duration(i) * time.Second),
			Seconds:   5,
			App:       "code",
			AppName:   "Code",
			Title:     fmt.Sprintf("file-%d", i),
		})
	}
	for i := 0; i < maxPendingPresence+150; i++ {
		a.note("lock", base.Add(time.Duration(i)*time.Second))
	}
	for i := 0; i < maxPendingInputMinutes+100; i++ {
		m := base.Add(time.Duration(i) * time.Minute)
		a.input[m] = &inputMinute{Minute: m, Keys: i + 1, Clicks: 1}
	}

	if err := a.checkpointSnapshot(); err != nil {
		t.Fatalf("checkpointSnapshot: %v", err)
	}

	recovered := &agent{
		spool: spool,
		input: map[time.Time]*inputMinute{},
	}
	if err := recovered.loadSnapshot(); err != nil {
		t.Fatalf("loadSnapshot: %v", err)
	}

	if len(recovered.events) != maxPendingEvents {
		t.Fatalf("events len = %d, want %d", len(recovered.events), maxPendingEvents)
	}
	if recovered.events[0].Title != "file-250" {
		t.Fatalf("oldest kept event title = %q, want file-250", recovered.events[0].Title)
	}
	if len(recovered.presence) != maxPendingPresence {
		t.Fatalf("presence len = %d, want %d", len(recovered.presence), maxPendingPresence)
	}
	if len(recovered.input) != maxPendingInputMinutes {
		t.Fatalf("input len = %d, want %d", len(recovered.input), maxPendingInputMinutes)
	}
	oldestKeptMinute := base.Add(100 * time.Minute)
	if recovered.input[oldestKeptMinute] == nil {
		t.Fatalf("expected minute %v to be retained", oldestKeptMinute)
	}
}

func TestSnapshotMalformedDataRecovery(t *testing.T) {
	spool := t.TempDir()
	base := time.Date(2026, 10, 21, 10, 0, 0, 0, time.UTC)

	// Write a valid snapshot first, then update so .bak is populated.
	a := &agent{
		spool: spool,
		input: map[time.Time]*inputMinute{},
		events: []event{
			{StartedAt: base, Seconds: 20, App: "notepad", AppName: "Notepad", Title: "notes.txt"},
		},
	}
	if err := a.checkpointSnapshot(); err != nil {
		t.Fatalf("first checkpoint: %v", err)
	}
	a.events = append(a.events, event{StartedAt: base.Add(time.Minute), Seconds: 40, App: "code", AppName: "Code", Title: "app.go"})
	if err := a.checkpointSnapshot(); err != nil {
		t.Fatalf("second checkpoint: %v", err)
	}

	// Corrupt the primary file; loadSnapshot must fall back to .bak without failing.
	if err := os.WriteFile(a.snapshotPath(), []byte("{corrupted-json"), 0o600); err != nil {
		t.Fatalf("corrupt primary: %v", err)
	}

	r1 := &agent{spool: spool, input: map[time.Time]*inputMinute{}}
	if err := r1.loadSnapshot(); err != nil {
		t.Fatalf("loadSnapshot with backup: %v", err)
	}
	if len(r1.events) != 1 || r1.events[0].App != "notepad" {
		t.Fatalf("expected backup recovery of 1 event, got %+v", r1.events)
	}

	// Corrupt both primary and backup; loadSnapshot must recover cleanly to empty state.
	if err := os.WriteFile(a.snapshotPath(), []byte("not-json"), 0o600); err != nil {
		t.Fatalf("corrupt primary: %v", err)
	}
	if err := os.WriteFile(a.snapshotPath()+".bak", []byte("also-corrupt"), 0o600); err != nil {
		t.Fatalf("corrupt backup: %v", err)
	}

	r2 := &agent{spool: spool, input: map[time.Time]*inputMinute{}}
	if err := r2.loadSnapshot(); err != nil {
		t.Fatalf("loadSnapshot with both corrupt: %v", err)
	}
	if len(r2.events) != 0 || len(r2.presence) != 0 || len(r2.input) != 0 {
		t.Fatalf("expected clean empty state, got events=%d presence=%d input=%d", len(r2.events), len(r2.presence), len(r2.input))
	}
	if _, err := os.Stat(a.snapshotPath()); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected corrupt primary snapshot to be removed, stat err = %v", err)
	}
}

func TestBoundedCaptureSpool(t *testing.T) {
	spool := t.TempDir()
	a := &agent{
		spool: spool,
		input: map[time.Time]*inputMinute{},
		events: []event{
			{StartedAt: time.Now().UTC(), Seconds: 10, App: "code", AppName: "Code", Title: "main.go"},
		},
	}
	if err := a.checkpointSnapshot(); err != nil {
		t.Fatalf("checkpointSnapshot: %v", err)
	}

	payload := make([]byte, 1024)
	files := []string{
		filepath.Join(spool, "screenshot-20261021T100000.jpg"),
		filepath.Join(spool, "screenshot-20261021T100500.jpg"),
		filepath.Join(spool, "recording-20261021T101000.webm"),
	}
	for _, f := range files {
		if err := os.WriteFile(f, payload, 0o600); err != nil {
			t.Fatalf("write %s: %v", f, err)
		}
		writeMeta(f, map[string]string{"app": "Code"})
	}

	// Limit spool to 2500 bytes: oldest screenshot must be evicted while newest 2 items and
	// pending-state.json remain intact.
	if err := enforceSpoolCap(spool, 2500); err != nil {
		t.Fatalf("enforceSpoolCap: %v", err)
	}

	if _, err := os.Stat(files[0]); !os.IsNotExist(err) {
		t.Fatalf("expected oldest capture %s to be evicted", files[0])
	}
	if _, err := os.Stat(files[0] + ".json"); !os.IsNotExist(err) {
		t.Fatalf("expected oldest capture metadata %s.json to be evicted", files[0])
	}
	for _, keep := range files[1:] {
		if _, err := os.Stat(keep); err != nil {
			t.Fatalf("expected newer capture %s to remain: %v", keep, err)
		}
	}
	if _, err := os.Stat(a.snapshotPath()); err != nil {
		t.Fatalf("pending-state.json must never be evicted by capture spool cap: %v", err)
	}
}

func TestWindowsWTSStructLayoutAndConversions(t *testing.T) {
	if wtsInfoWSize != 216 {
		t.Fatalf("sizeof(wtsInfoW) = %d, want 216", wtsInfoWSize)
	}
	if wtsInfoExLevel1WSize != 224 {
		t.Fatalf("sizeof(wtsInfoExLevel1W) = %d, want 224", wtsInfoExLevel1WSize)
	}
	if wtsInfoExWSize != 232 {
		t.Fatalf("sizeof(wtsInfoExW) = %d, want 232", wtsInfoExWSize)
	}

	var info wtsInfoW
	if got := unsafe.Offsetof(info.ConnectTime); got != 176 {
		t.Fatalf("wtsInfoW.ConnectTime offset = %d, want 176 (8-byte aligned)", got)
	}
	if got := unsafe.Offsetof(info.LogonTime); got != 200 {
		t.Fatalf("wtsInfoW.LogonTime offset = %d, want 200", got)
	}

	var ex wtsInfoExW
	if got := unsafe.Offsetof(ex.Data); got != 8 {
		t.Fatalf("wtsInfoExW.Data offset = %d, want 8", got)
	}
	if got := unsafe.Offsetof(ex.Data.LogonTime); got != 160 {
		t.Fatalf("wtsInfoExLevel1W.LogonTime offset = %d, want 160 (8-byte aligned)", got)
	}

	var nid notifyIconDataW
	setNotifyIconTip(&nid, "HR Lens Agent — Monitoring Active")
	if got := utf16SliceToString(nid.SzTip[:]); got != "HR Lens Agent — Monitoring Active" {
		t.Fatalf("notifyIconDataW tip = %q", got)
	}

	wantTime := time.Date(2026, 10, 21, 8, 30, 45, 0, time.UTC)
	ticks := windowsEpochTicks + wantTime.Unix()*ticksPerSecond

	ex.Level = 1
	ex.Data.SessionId = 3
	ex.Data.SessionState = wtsActive
	ex.Data.SessionFlags = wtsSessionStateLock
	ex.Data.LogonTime = ticks
	copy(ex.Data.UserName[:], utf16.Encode([]rune("alice")))
	copy(ex.Data.DomainName[:], utf16.Encode([]rune("CORP")))
	copy(ex.Data.WinStationName[:], utf16.Encode([]rune("Console")))

	rawEx := unsafe.Slice((*byte)(unsafe.Pointer(&ex)), wtsInfoExWSize)
	parsedEx, err := parseWTSInfoEx(rawEx)
	if err != nil {
		t.Fatalf("parseWTSInfoEx: %v", err)
	}
	if parsedEx.SessionID != 3 || !parsedEx.LockKnown || !parsedEx.Locked || parsedEx.Disconnected {
		t.Fatalf("unexpected parsedEx state: %+v", parsedEx)
	}
	if !parsedEx.HasLogonTime || !parsedEx.LogonTime.Equal(wantTime) {
		t.Fatalf("parsedEx.LogonTime = %v, want %v", parsedEx.LogonTime, wantTime)
	}
	if parsedEx.UserName != "alice" || parsedEx.Domain != "CORP" || parsedEx.WinStation != "Console" {
		t.Fatalf("unexpected strings in parsedEx: %+v", parsedEx)
	}

	info.State = wtsDisconnected
	info.SessionId = 7
	info.LogonTime = ticks
	copy(info.UserName[:], utf16.Encode([]rune("bob")))
	rawInfo := unsafe.Slice((*byte)(unsafe.Pointer(&info)), wtsInfoWSize)
	parsedInfo, err := parseWTSInfo(rawInfo)
	if err != nil {
		t.Fatalf("parseWTSInfo: %v", err)
	}
	if parsedInfo.SessionID != 7 || !parsedInfo.Disconnected || !parsedInfo.HasLogonTime || !parsedInfo.LogonTime.Equal(wantTime) || parsedInfo.UserName != "bob" {
		t.Fatalf("unexpected parsedInfo: %+v", parsedInfo)
	}
}

func TestSessionLifecycleStateSeparatesUACFromAttendanceLock(t *testing.T) {
	var st sessionLifecycleState
	logon := time.Date(2026, 10, 21, 9, 15, 0, 0, time.UTC)
	st.initFromWTS(parsedWTSSession{
		SessionID:    2,
		ConnectState: wtsActive,
		Locked:       false,
		LockKnown:    true,
		Disconnected: false,
		LogonTime:    logon,
		HasLogonTime: true,
	})

	if st.attendanceLocked() {
		t.Fatal("expected session unlocked initially")
	}
	if st.captureSuspended(false) || !st.canCaptureNow(false) {
		t.Fatal("expected capture active on default desktop")
	}

	// UAC prompt (secureDesktop == true) must suspend capture without changing attendanceLocked.
	if !st.captureSuspended(true) || st.canCaptureNow(true) {
		t.Fatal("expected capture suspended during UAC secure desktop")
	}
	if st.attendanceLocked() {
		t.Fatal("UAC secure desktop must never count as attendance lock")
	}

	// Actual WTS lock changes attendanceLocked and suspends capture even if secureDesktop is false.
	st.applyWTSSessionChange(wtsSessionLock)
	if !st.attendanceLocked() || !st.captureSuspended(false) {
		t.Fatal("expected attendanceLocked and captureSuspended after WTS_SESSION_LOCK")
	}
	st.applyWTSSessionChange(wtsSessionUnlock)
	if st.attendanceLocked() || st.captureSuspended(false) {
		t.Fatal("expected unlocked and capture resumed after WTS_SESSION_UNLOCK")
	}

	// Session disconnect and power suspend pause capture without reporting a lock event.
	st.applyWTSSessionChange(wtsConsoleDisconnect)
	if st.attendanceLocked() || !st.captureSuspended(false) {
		t.Fatal("expected disconnected session to suspend capture without locking attendance")
	}
	st.applyWTSSessionChange(wtsConsoleConnect)
	if st.captureSuspended(false) {
		t.Fatal("expected capture resumed after WTS_CONSOLE_CONNECT")
	}

	st.applyPowerBroadcast(pbtAPMSuspend)
	if st.attendanceLocked() || !st.captureSuspended(false) {
		t.Fatal("expected power suspend to pause capture without locking attendance")
	}
	st.applyPowerBroadcast(pbtAPMResumeAutomatic)
	if st.captureSuspended(false) {
		t.Fatal("expected capture resumed after PBT_APMRESUMEAUTOMATIC")
	}

	if !st.applyWTSSessionChange(wtsSessionLogoff) {
		t.Fatal("expected WTS_SESSION_LOGOFF to request agent stop")
	}
}

func TestCapabilitiesAndFlushCheckpointPersistence(t *testing.T) {
	var gotHeartbeatCaps map[string]bool
	failActivity := true

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/agent/heartbeat":
			var body struct {
				Capabilities map[string]bool `json:"capabilities"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotHeartbeatCaps = body.Capabilities
			_ = json.NewEncoder(w).Encode(map[string]any{"live": false, "screenshotEvery": 300})
		case "/api/agent/presence", "/api/agent/input":
			w.WriteHeader(http.StatusNoContent)
		case "/api/agent/activity":
			if failActivity {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)
		}
	}))
	defer srv.Close()

	spool := t.TempDir()
	a := &agent{
		cfg:   config{Server: srv.URL, Token: "test-token"},
		log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
		http:  srv.Client(),
		spool: spool,
		caps:  initialCapabilities(),
		input: map[time.Time]*inputMinute{},
		tabs:  newTabs(),
	}

	if runtime.GOOS == "windows" {
		if a.capabilities().Screenshots || a.capabilities().Recordings || a.capabilities().LiveScreen || a.capabilities().InputCounts {
			t.Fatalf("expected Windows initial capture/input capabilities to be false: %+v", a.capabilities())
		}
	}

	a.setRecordingsCapability(false)
	a.setScreenshotsCapability(true)
	a.setInputCountsCapability(true)
	a.setBrowserDomainsCapability(true)

	var out struct {
		Live            bool `json:"live"`
		ScreenshotEvery int  `json:"screenshotEvery"`
	}
	if err := a.postJSON(context.Background(), "/api/agent/heartbeat", map[string]any{"capabilities": a.capabilities()}, &out); err != nil {
		t.Fatalf("heartbeat postJSON: %v", err)
	}
	if !gotHeartbeatCaps["screenshots"] || !gotHeartbeatCaps["input_counts"] || !gotHeartbeatCaps["browser_domains"] || gotHeartbeatCaps["recordings"] {
		t.Fatalf("unexpected heartbeat capabilities payload: %+v", gotHeartbeatCaps)
	}

	// Queue activity and attempt flush while server fails; snapshot must retain pending events.
	now := time.Now().UTC().Truncate(time.Minute)
	a.events = []event{{StartedAt: now, Seconds: 25, App: "code", AppName: "Code", Title: "main.go"}}
	a.flushActivity(context.Background())

	check1 := &agent{spool: spool, input: map[time.Time]*inputMinute{}}
	if err := check1.loadSnapshot(); err != nil {
		t.Fatalf("loadSnapshot after failed flush: %v", err)
	}
	if len(check1.events) != 1 {
		t.Fatalf("expected 1 event persisted after failed flush, got %d", len(check1.events))
	}

	// Succeed on retry; snapshot must clear acknowledged events.
	failActivity = false
	a.flushActivity(context.Background())

	check2 := &agent{spool: spool, input: map[time.Time]*inputMinute{}}
	if err := check2.loadSnapshot(); err != nil {
		t.Fatalf("loadSnapshot after successful flush: %v", err)
	}
	if len(check2.events) != 0 {
		t.Fatalf("expected 0 events in snapshot after ack, got %d", len(check2.events))
	}
}
