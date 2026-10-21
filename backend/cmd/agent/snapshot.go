package main

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf16"
	"unsafe"

	"hrlens/backend/internal/capability"
)

const (
	// checkpointEvery bounds the maximum in-progress activity/counter loss window on an abrupt
	// crash or power loss. Pending batches are also persisted before upload and after server ack,
	// providing at-least-once delivery until the server protocol adds idempotent batch IDs.
	checkpointEvery = 5 * time.Second

	maxPendingEvents       = 5000
	maxPendingPresence     = 1000
	maxPendingInputMinutes = 1440

	defaultMaxSpoolBytes int64 = 1 << 30 // 1 GiB default capture spool cap

	pendingSnapshotFile = "pending-state.json"
)

// initialCapabilities returns the static capabilities advertised during registration and at
// agent startup before runtime hooks or browser extension presence update them.
// Windows capture and input hooks remain false until the Windows capture stage is enabled.
func initialCapabilities() capability.Report {
	if runtime.GOOS == "linux" {
		return capability.Report{
			Screenshots: canCapture,
			Recordings:  canCapture,
			LiveScreen:  canCapture,
			InputCounts: true,
		}
	}
	return capability.Report{}
}

func (a *agent) capabilities() capability.Report {
	a.mu.Lock()
	r := a.caps
	tabs := a.tabs
	a.mu.Unlock()
	if tabs != nil && tabs.recent() {
		r.BrowserDomains = true
	}
	return r
}

func (a *agent) setScreenshotsCapability(enabled bool) {
	a.mu.Lock()
	a.caps.Screenshots = enabled
	a.mu.Unlock()
}

func (a *agent) setRecordingsCapability(enabled bool) {
	a.mu.Lock()
	a.caps.Recordings = enabled
	a.mu.Unlock()
}

func (a *agent) setLiveScreenCapability(enabled bool) {
	a.mu.Lock()
	a.caps.LiveScreen = enabled
	a.mu.Unlock()
}

func (a *agent) setInputCountsCapability(enabled bool) {
	a.mu.Lock()
	a.caps.InputCounts = enabled
	a.mu.Unlock()
}

func (a *agent) setBrowserDomainsCapability(enabled bool) {
	a.mu.Lock()
	a.caps.BrowserDomains = enabled
	a.mu.Unlock()
}

// sessionSpoolDir scopes the spool directory per desktop session when the platform reports a
// session key (such as the Windows session ID), preventing two sessions of the same user from
// racing on pending snapshots or media files.
func sessionSpoolDir(configDir, sessionKey string) string {
	base := filepath.Join(configDir, "spool")
	key := strings.TrimSpace(sessionKey)
	if key == "" {
		return base
	}
	return filepath.Join(base, key)
}

type pendingSnapshot struct {
	Version     int           `json:"version"`
	SavedAt     time.Time     `json:"savedAt"`
	LastLoginAt time.Time     `json:"lastLoginAt,omitempty"`
	Open        *event        `json:"open,omitempty"`
	Events      []event       `json:"events,omitempty"`
	Presence    []presence    `json:"presence,omitempty"`
	Input       []inputMinute `json:"input,omitempty"`
}

func (a *agent) snapshotPath() string {
	return filepath.Join(a.spool, pendingSnapshotFile)
}

// copySnapshotLocked copies pending state while holding a.mu so JSON marshaling and disk I/O
// happen outside a.mu, keeping input and lifecycle callbacks non-blocking.
func (a *agent) copySnapshotLocked(now time.Time) pendingSnapshot {
	events := make([]event, 0, len(a.events))
	for _, ev := range a.events {
		if ev.Seconds > 0 && ev.App != "" && !ev.StartedAt.IsZero() {
			events = append(events, ev)
		}
	}
	if len(events) > maxPendingEvents {
		events = append([]event(nil), events[len(events)-maxPendingEvents:]...)
	}

	pres := make([]presence, 0, len(a.presence))
	for _, p := range a.presence {
		if p.Kind != "" && !p.At.IsZero() {
			pres = append(pres, p)
		}
	}
	if len(pres) > maxPendingPresence {
		pres = append([]presence(nil), pres[len(pres)-maxPendingPresence:]...)
	}

	minutes := make([]inputMinute, 0, len(a.input))
	for _, m := range a.input {
		if m != nil && !m.Minute.IsZero() && (m.Keys > 0 || m.Clicks > 0) {
			minutes = append(minutes, *m)
		}
	}
	sort.Slice(minutes, func(i, j int) bool {
		return minutes[i].Minute.Before(minutes[j].Minute)
	})
	if len(minutes) > maxPendingInputMinutes {
		minutes = append([]inputMinute(nil), minutes[len(minutes)-maxPendingInputMinutes:]...)
	}

	var openCopy *event
	if a.open != nil && a.open.App != "" && !a.open.StartedAt.IsZero() {
		cp := *a.open
		if cp.Seconds <= 0 && now.After(cp.StartedAt) {
			cp.Seconds = int(now.Sub(cp.StartedAt).Round(time.Second).Seconds())
		}
		if cp.Seconds > 0 {
			openCopy = &cp
		}
	}

	return pendingSnapshot{
		Version:     1,
		SavedAt:     now.UTC(),
		LastLoginAt: a.lastLoginAt,
		Open:        openCopy,
		Events:      events,
		Presence:    pres,
		Input:       minutes,
	}
}

// checkpointSnapshot sequences snapshot writes with a.snapMu and copies in-memory state under
// a.mu before marshaling and syncing to disk outside a.mu.
func (a *agent) checkpointSnapshot() error {
	if a.spool == "" {
		return nil
	}
	a.snapMu.Lock()
	defer a.snapMu.Unlock()

	a.mu.Lock()
	snap := a.copySnapshotLocked(time.Now())
	a.mu.Unlock()

	data, err := json.Marshal(snap)
	if err != nil {
		return err
	}
	return atomicWriteFileWithBackup(a.snapshotPath(), data, 0o600)
}

func (a *agent) checkpoints(ctx context.Context) {
	every(ctx, checkpointEvery, func() {
		if err := a.checkpointSnapshot(); err != nil && a.log != nil {
			a.log.Warn("checkpointing pending state failed", "error", err)
		}
	})
}

// loadSnapshot recovers any unsent events, presence, and per-minute input counts from disk.
// If the primary snapshot file is corrupted, it falls back to the backup file or resets cleanly.
func (a *agent) loadSnapshot() error {
	if a.spool == "" {
		return nil
	}
	a.snapMu.Lock()
	defer a.snapMu.Unlock()

	path := a.snapshotPath()
	snap, err := readSnapshotFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			if a.log != nil {
				a.log.Warn("pending state snapshot is malformed; attempting backup recovery", "error", err)
			}
			_ = removeIfExists(path)
		}
		bakSnap, bakErr := readSnapshotFile(path + ".bak")
		if bakErr != nil {
			if !errors.Is(bakErr, os.ErrNotExist) {
				_ = removeIfExists(path + ".bak")
			}
			return nil
		}
		snap = bakSnap
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	if !snap.LastLoginAt.IsZero() {
		a.lastLoginAt = snap.LastLoginAt
	}

	events := make([]event, 0, len(snap.Events)+1)
	for _, ev := range snap.Events {
		if ev.Seconds > 0 && ev.App != "" && !ev.StartedAt.IsZero() {
			events = append(events, ev)
		}
	}
	if snap.Open != nil && snap.Open.Seconds > 0 && snap.Open.App != "" && !snap.Open.StartedAt.IsZero() {
		events = append(events, *snap.Open)
	}
	events = append(events, a.events...)
	if len(events) > maxPendingEvents {
		events = events[len(events)-maxPendingEvents:]
	}
	a.events = events

	pres := make([]presence, 0, len(snap.Presence)+len(a.presence))
	for _, p := range snap.Presence {
		if p.Kind != "" && !p.At.IsZero() {
			pres = append(pres, p)
			if p.Kind == "login" && a.lastLoginAt.IsZero() {
				a.lastLoginAt = p.At
			}
		}
	}
	pres = append(pres, a.presence...)
	if len(pres) > maxPendingPresence {
		pres = pres[len(pres)-maxPendingPresence:]
	}
	a.presence = pres

	if a.input == nil {
		a.input = map[time.Time]*inputMinute{}
	}
	for _, m := range snap.Input {
		if m.Minute.IsZero() || (m.Keys <= 0 && m.Clicks <= 0) {
			continue
		}
		minute := m.Minute.Truncate(time.Minute)
		existing := a.input[minute]
		if existing == nil {
			cp := inputMinute{
				Minute: minute,
				Keys:   max(0, m.Keys),
				Clicks: max(0, m.Clicks),
			}
			a.input[minute] = &cp
		} else {
			existing.Keys += max(0, m.Keys)
			existing.Clicks += max(0, m.Clicks)
		}
	}
	trimInputMapLocked(a.input)
	return nil
}

func trimInputMapLocked(input map[time.Time]*inputMinute) {
	if len(input) <= maxPendingInputMinutes {
		return
	}
	minutes := make([]time.Time, 0, len(input))
	for k := range input {
		minutes = append(minutes, k)
	}
	sort.Slice(minutes, func(i, j int) bool {
		return minutes[i].Before(minutes[j])
	})
	drop := len(minutes) - maxPendingInputMinutes
	for i := 0; i < drop; i++ {
		delete(input, minutes[i])
	}
}

func readSnapshotFile(path string) (pendingSnapshot, error) {
	f, err := os.Open(path)
	if err != nil {
		return pendingSnapshot{}, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if err != nil {
		return pendingSnapshot{}, err
	}
	if len(data) > 32<<20 || !json.Valid(data) {
		return pendingSnapshot{}, errors.New("invalid or oversized snapshot")
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return pendingSnapshot{}, errors.New("empty snapshot file")
	}
	var snap pendingSnapshot
	dec := json.NewDecoder(bytes.NewReader(data))
	if err := dec.Decode(&snap); err != nil {
		return pendingSnapshot{}, err
	}
	if snap.Version != 1 {
		return pendingSnapshot{}, errors.New("unsupported snapshot version")
	}
	return snap, nil
}

// noteLogin records the desktop session sign-in time after snapshot recovery, deduplicating
// against a recovered pending login from the same session start.
func (a *agent) noteLogin(signedIn time.Time, knownSessionStart bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !knownSessionStart && !a.lastLoginAt.IsZero() {
		signedIn = a.lastLoginAt
	}
	for _, p := range a.presence {
		if p.Kind == "login" {
			diff := p.At.Sub(signedIn)
			if diff < 0 {
				diff = -diff
			}
			if diff <= time.Second || !knownSessionStart {
				a.lastLoginAt = p.At
				return
			}
		}
	}
	if !a.lastLoginAt.IsZero() {
		diff := a.lastLoginAt.Sub(signedIn)
		if diff < 0 {
			diff = -diff
		}
		if diff <= time.Second {
			return
		}
	}
	a.lastLoginAt = signedIn
	a.presence = append(a.presence, presence{Kind: "login", At: signedIn})
	if len(a.presence) > maxPendingPresence {
		a.presence = a.presence[len(a.presence)-maxPendingPresence:]
	}
}

// atomicWriteFile writes data to a temporary file in the same directory, flushes it to disk,
// closes the handle (required on Windows before rename), and atomically replaces path.
func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, perm)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := replaceStateFile(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func atomicWriteFileWithBackup(path string, data []byte, perm os.FileMode) error {
	if existing, err := os.ReadFile(path); err == nil && json.Valid(existing) {
		_ = atomicWriteFile(path+".bak", existing, perm)
	}
	return atomicWriteFile(path, data, perm)
}

func configuredMaxSpoolBytes() int64 {
	if v, err := strconv.ParseInt(strings.TrimSpace(os.Getenv("HRLENS_MAX_SPOOL_BYTES")), 10, 64); err == nil && v > 0 {
		return v
	}
	return defaultMaxSpoolBytes
}

func (a *agent) spoolLimitBytes() int64 {
	if a.maxSpoolBytes > 0 {
		return a.maxSpoolBytes
	}
	return configuredMaxSpoolBytes()
}

func (a *agent) enforceSpoolLimit() {
	if a.spool == "" {
		return
	}
	a.spoolMu.Lock()
	defer a.spoolMu.Unlock()
	if err := enforceSpoolCap(a.spool, a.spoolLimitBytes()); err != nil && a.log != nil {
		a.log.Warn("bounding media spool failed", "error", err)
	}
}

type spoolItem struct {
	base    string
	modTime time.Time
	size    int64
}

// enforceSpoolCap bounds the total size of captured media files (.jpg, .webm and companion
// metadata/receipts) in spoolDir to maxBytes by evicting the oldest captures first.
// It never removes pending-state.json or live frame scratch files.
func enforceSpoolCap(spoolDir string, maxBytes int64) error {
	if maxBytes <= 0 {
		maxBytes = defaultMaxSpoolBytes
	}
	entries, err := os.ReadDir(spoolDir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}

	var items []spoolItem
	var total int64
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := filepath.Ext(name)
		if ext != ".jpg" && ext != ".webm" {
			continue
		}
		full := filepath.Join(spoolDir, name)
		info, err := entry.Info()
		if err != nil {
			continue
		}
		itemSize := info.Size()
		if metaInfo, err := os.Stat(full + ".json"); err == nil {
			itemSize += metaInfo.Size()
		}
		if upInfo, err := os.Stat(uploadReceiptPath(full)); err == nil {
			itemSize += upInfo.Size()
		}
		items = append(items, spoolItem{
			base:    full,
			modTime: info.ModTime(),
			size:    itemSize,
		})
		total += itemSize
	}

	if total <= maxBytes {
		return nil
	}

	sort.Slice(items, func(i, j int) bool {
		if !items[i].modTime.Equal(items[j].modTime) {
			return items[i].modTime.Before(items[j].modTime)
		}
		return items[i].base < items[j].base
	})

	for _, item := range items {
		if total <= maxBytes {
			break
		}
		if err := removeUpload(item.base); err != nil {
			return err
		}
		total -= item.size
	}
	return nil
}

// ---------- Portable Windows WTS Structures, Conversions & Session State ----------

const (
	wtsActive       int32 = 0
	wtsConnected    int32 = 1
	wtsConnectQuery int32 = 2
	wtsShadow       int32 = 3
	wtsDisconnected int32 = 4
	wtsIdle         int32 = 5

	wtsSessionStateUnknown int32 = -1
	wtsSessionStateLock    int32 = 0
	wtsSessionStateUnlock  int32 = 1

	// Windows WM_WTSSESSION_CHANGE wParam notification codes
	wtsConsoleConnect    uintptr = 1
	wtsConsoleDisconnect uintptr = 2
	wtsRemoteConnect     uintptr = 3
	wtsRemoteDisconnect  uintptr = 4
	wtsSessionLogon      uintptr = 5
	wtsSessionLogoff     uintptr = 6
	wtsSessionLock       uintptr = 7
	wtsSessionUnlock     uintptr = 8

	// Windows WM_POWERBROADCAST wParam codes
	pbtAPMSuspend         uintptr = 0x0004
	pbtAPMResumeCritical  uintptr = 0x0006
	pbtAPMResumeSuspend   uintptr = 0x0007
	pbtAPMResumeAutomatic uintptr = 0x0012

	// 100-nanosecond intervals between 1601-01-01 UTC (Windows FILETIME epoch) and 1970-01-01 UTC
	windowsEpochTicks int64 = 116444736000000000
	ticksPerSecond    int64 = 10_000_000
)

// wtsInfoW matches the Windows SDK WTSINFOW layout (216 bytes, 8-byte aligned LARGE_INTEGERs).
type wtsInfoW struct {
	State                   int32
	SessionId               uint32
	IncomingBytes           uint32
	OutgoingBytes           uint32
	IncomingFrames          uint32
	OutgoingFrames          uint32
	IncomingCompressedBytes uint32
	OutgoingCompressedBytes uint32
	WinStationName          [32]uint16
	Domain                  [17]uint16
	UserName                [21]uint16
	_                       [4]byte // explicit 8-byte alignment padding before LARGE_INTEGER ConnectTime
	ConnectTime             int64
	DisconnectTime          int64
	LastInputTime           int64
	LogonTime               int64
	CurrentTime             int64
}

// wtsInfoExLevel1W matches the Windows SDK WTSINFOEX_LEVEL1_W layout (224 bytes).
type wtsInfoExLevel1W struct {
	SessionId               uint32
	SessionState            int32
	SessionFlags            int32
	WinStationName          [33]uint16
	UserName                [21]uint16
	DomainName              [18]uint16
	_                       [4]byte // explicit 8-byte alignment padding before LARGE_INTEGER LogonTime
	LogonTime               int64
	ConnectTime             int64
	DisconnectTime          int64
	LastInputTime           int64
	CurrentTime             int64
	IncomingBytes           uint32
	OutgoingBytes           uint32
	IncomingFrames          uint32
	OutgoingFrames          uint32
	IncomingCompressedBytes uint32
	OutgoingCompressedBytes uint32
}

// wtsInfoExW matches the Windows SDK WTSINFOEXW layout (232 bytes).
type wtsInfoExW struct {
	Level uint32
	_     [4]byte // explicit 8-byte alignment padding before union Data
	Data  wtsInfoExLevel1W
}

const (
	wtsInfoWSize         = int(unsafe.Sizeof(wtsInfoW{}))
	wtsInfoExLevel1WSize = int(unsafe.Sizeof(wtsInfoExLevel1W{}))
	wtsInfoExWSize       = int(unsafe.Sizeof(wtsInfoExW{}))
)

type winGUID struct {
	Data1 uint32
	Data2 uint16
	Data3 uint16
	Data4 [8]byte
}

// notifyIconDataW matches the Windows SDK NOTIFYICONDATAW layout for Shell_NotifyIconW.
type notifyIconDataW struct {
	CbSize           uint32
	HWnd             uintptr
	UID              uint32
	UFlags           uint32
	UCallbackMessage uint32
	HIcon            uintptr
	SzTip            [128]uint16
	DwState          uint32
	DwStateMask      uint32
	SzInfo           [256]uint16
	UVersion         uint32
	SzInfoTitle      [64]uint16
	DwInfoFlags      uint32
	GuidItem         winGUID
	HBalloonIcon     uintptr
}

func setNotifyIconTip(nid *notifyIconDataW, tip string) {
	encoded := utf16.Encode([]rune(tip))
	if len(encoded) >= len(nid.SzTip) {
		encoded = encoded[:len(nid.SzTip)-1]
	}
	clear(nid.SzTip[:])
	copy(nid.SzTip[:], encoded)
}

// filetimeTicksToTime converts a Windows FILETIME / LARGE_INTEGER value (100ns ticks since
// 1601-01-01 UTC) into a UTC time.Time.
func filetimeTicksToTime(ticks int64) (time.Time, bool) {
	if ticks <= windowsEpochTicks {
		return time.Time{}, false
	}
	unixTicks := ticks - windowsEpochTicks
	sec := unixTicks / ticksPerSecond
	nsec := (unixTicks % ticksPerSecond) * 100
	t := time.Unix(sec, nsec).UTC()
	if t.Year() < 2000 || t.Year() > 2200 {
		return time.Time{}, false
	}
	return t, true
}

func utf16SliceToString(buf []uint16) string {
	for i, v := range buf {
		if v == 0 {
			buf = buf[:i]
			break
		}
	}
	return string(utf16.Decode(buf))
}

type parsedWTSSession struct {
	SessionID    uint32
	ConnectState int32
	Locked       bool
	LockKnown    bool
	Disconnected bool
	LogonTime    time.Time
	HasLogonTime bool
	UserName     string
	Domain       string
	WinStation   string
}

func parseWTSInfoEx(raw []byte) (parsedWTSSession, error) {
	if len(raw) < wtsInfoExWSize {
		return parsedWTSSession{}, fmt.Errorf("WTSINFOEXW buffer too short: got %d bytes, want at least %d", len(raw), wtsInfoExWSize)
	}
	level := binary.LittleEndian.Uint32(raw[0:4])
	if level != 1 {
		return parsedWTSSession{}, fmt.Errorf("unsupported WTSINFOEXW level %d", level)
	}
	var info wtsInfoExW
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&info)), wtsInfoExWSize), raw[:wtsInfoExWSize])

	d := info.Data
	out := parsedWTSSession{
		SessionID:    d.SessionId,
		ConnectState: d.SessionState,
		Disconnected: d.SessionState != wtsActive,
		UserName:     utf16SliceToString(d.UserName[:]),
		Domain:       utf16SliceToString(d.DomainName[:]),
		WinStation:   utf16SliceToString(d.WinStationName[:]),
	}
	switch d.SessionFlags {
	case wtsSessionStateLock:
		out.Locked = true
		out.LockKnown = true
	case wtsSessionStateUnlock:
		out.Locked = false
		out.LockKnown = true
	}
	if logon, ok := filetimeTicksToTime(d.LogonTime); ok {
		out.LogonTime = logon
		out.HasLogonTime = true
	}
	return out, nil
}

func parseWTSInfo(raw []byte) (parsedWTSSession, error) {
	if len(raw) < wtsInfoWSize {
		return parsedWTSSession{}, fmt.Errorf("WTSINFOW buffer too short: got %d bytes, want at least %d", len(raw), wtsInfoWSize)
	}
	var info wtsInfoW
	copy(unsafe.Slice((*byte)(unsafe.Pointer(&info)), wtsInfoWSize), raw[:wtsInfoWSize])

	out := parsedWTSSession{
		SessionID:    info.SessionId,
		ConnectState: info.State,
		Disconnected: info.State != wtsActive,
		UserName:     utf16SliceToString(info.UserName[:]),
		Domain:       utf16SliceToString(info.Domain[:]),
		WinStation:   utf16SliceToString(info.WinStationName[:]),
	}
	if logon, ok := filetimeTicksToTime(info.LogonTime); ok {
		out.LogonTime = logon
		out.HasLogonTime = true
	}
	return out, nil
}

// sessionLifecycleState tracks actual WTS session lock state separately from temporary capture
// suspension (UAC/secure desktop, disconnected session, or system power suspend).
// Only WTS session lock/unlock transitions change attendanceLocked; UAC never counts as lock/unlock.
type sessionLifecycleState struct {
	mu                  sync.RWMutex
	sessionID           uint32
	hasSessionID        bool
	sessionLocked       bool
	sessionDisconnected bool
	powerSuspended      bool
	ending              bool
	logonTime           time.Time
}

func (st *sessionLifecycleState) initFromWTS(parsed parsedWTSSession) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.sessionID = parsed.SessionID
	st.hasSessionID = true
	if parsed.LockKnown {
		st.sessionLocked = parsed.Locked
	}
	st.sessionDisconnected = parsed.Disconnected
	if parsed.HasLogonTime {
		st.logonTime = parsed.LogonTime
	}
}

func (st *sessionLifecycleState) applyWTSSessionChange(code uintptr) (stopRequested bool) {
	st.mu.Lock()
	defer st.mu.Unlock()
	switch code {
	case wtsSessionLock:
		st.sessionLocked = true
	case wtsSessionUnlock:
		st.sessionLocked = false
	case wtsConsoleDisconnect, wtsRemoteDisconnect:
		st.sessionDisconnected = true
	case wtsConsoleConnect, wtsRemoteConnect:
		st.sessionDisconnected = false
	case wtsSessionLogoff:
		st.sessionDisconnected = true
		return true
	}
	return false
}

func (st *sessionLifecycleState) applyPowerBroadcast(code uintptr) {
	st.mu.Lock()
	defer st.mu.Unlock()
	switch code {
	case pbtAPMSuspend:
		st.powerSuspended = true
	case pbtAPMResumeAutomatic, pbtAPMResumeSuspend, pbtAPMResumeCritical:
		st.powerSuspended = false
	}
}

func (st *sessionLifecycleState) attendanceLocked() bool {
	st.mu.RLock()
	defer st.mu.RUnlock()
	return st.sessionLocked
}

func (st *sessionLifecycleState) captureSuspended(secureDesktop bool) bool {
	st.mu.RLock()
	locked := st.sessionLocked
	disconnected := st.sessionDisconnected
	suspended := st.powerSuspended || st.ending
	st.mu.RUnlock()
	return locked || disconnected || suspended || secureDesktop
}

func (st *sessionLifecycleState) canCaptureNow(secureDesktop bool) bool {
	return !st.captureSuspended(secureDesktop)
}

func (st *sessionLifecycleState) sessionStart() (time.Time, bool) {
	st.mu.RLock()
	defer st.mu.RUnlock()
	if st.logonTime.IsZero() {
		return time.Time{}, false
	}
	return st.logonTime, true
}
