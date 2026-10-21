package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"

	"hrlens/backend/internal/auth"
	"hrlens/backend/internal/capability"
)

// ---------- Desktop agent API ----------
//
// The agent signs in once with the person's email and password and gets a device token.
// Every later call sends it as "Authorization: Bearer <token>".

const (
	screenshotEverySec = 300              // tells the agent how often to take a screenshot
	maxScreenshotBytes = 15 << 20         // one screenshot
	maxRecordingBytes  = 300 << 20        // one recording piece
	maxLiveFrameBytes  = 3 << 20          // one live frame
	liveWatchWindow    = 20 * time.Second // a viewer counts as watching this long after their last request
	liveFrameMaxAge    = 15 * time.Second // older frames are not shown as live
	deviceOnlineWithin = 45 * time.Second // heartbeat age that still counts as online
	recordingUploadMax = 10 * time.Minute // time allowed to upload one recording piece
	maxClockAhead      = 2 * time.Minute  // capture times further in the future than this are replaced
)

type device struct {
	ID     int64
	UserID int64
	Name   string
}

type deviceKey struct{}

func deviceFrom(r *http.Request) device { return r.Context().Value(deviceKey{}).(device) }

// requireDevice runs next for a known, not revoked device of an active user.
func (s *Server) requireDevice(next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || token == "" {
			writeError(w, http.StatusUnauthorized, "Device token required")
			return
		}
		var d device
		err := s.db.QueryRow(r.Context(), `
			UPDATE devices d SET last_seen_at = now(), agent_version = COALESCE(NULLIF($2, ''), d.agent_version)
			FROM users u
			WHERE d.token_hash = $1 AND d.revoked_at IS NULL AND u.id = d.user_id AND u.status = 'active'
			RETURNING d.id, d.user_id, d.name`, auth.HashToken(token), agentVersion(r)).Scan(&d.ID, &d.UserID, &d.Name)
		if err != nil {
			writeError(w, http.StatusUnauthorized, "This device is not registered or was removed. Register it again.")
			return
		}
		next(w, r.WithContext(context.WithValue(r.Context(), deviceKey{}, d)))
	})
}

type registerRequest struct {
	Email        string             `json:"email"`
	Password     string             `json:"password"`
	DeviceName   string             `json:"deviceName"`
	OS           string             `json:"os"`
	AgentVersion string             `json:"agentVersion"`
	Capabilities *capability.Report `json:"capabilities"`
}

// POST /api/agent/register — body {email, password, deviceName, os, agentVersion}; returns {token, userId, name}
// Registering clears the person's seed sample activity, so their pages show only real data from then on.
func (s *Server) registerDevice(w http.ResponseWriter, r *http.Request) {
	var req registerRequest
	if !decode(w, r, &req) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	name := truncate(strings.TrimSpace(req.DeviceName), 120)
	if email == "" || req.Password == "" || name == "" {
		writeError(w, http.StatusBadRequest, "Enter the email, password and device name")
		return
	}
	key := "agent|" + clientIP(r) + "|" + email
	if ok, wait := s.limiter.Allowed(key); !ok {
		w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Try again later.")
		return
	}
	var userID int64
	var hash, status, fullName string
	err := s.db.QueryRow(r.Context(), `
		SELECT id, password_hash, status::text, trim(first_name || ' ' || last_name) FROM users WHERE lower(email) = $1`,
		email).Scan(&userID, &hash, &status, &fullName)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		s.fail(w, r, err)
		return
	}
	if !auth.CheckPassword(hash, req.Password) {
		s.limiter.Fail(key)
		writeError(w, http.StatusUnauthorized, "Email or password is incorrect")
		return
	}
	s.limiter.Reset(key)
	if status != "active" {
		writeError(w, http.StatusForbidden, "This account is inactive. Ask your administrator to activate it.")
		return
	}

	token, tokenHash, err := auth.NewToken()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	err = pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO devices (user_id, name, os, agent_version, token_hash, last_seen_at, capabilities)
			VALUES ($1, $2, $3, $4, $5, now(), $6)`,
			userID, name, truncate(req.OS, 60), truncate(req.AgentVersion, 30), tokenHash, capability.JSON(req.Capabilities)); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `DELETE FROM activity_events WHERE user_id = $1 AND device_id IS NULL`, userID)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("device registered", "user", userID, "device", name)
	writeJSON(w, http.StatusCreated, map[string]any{"token": token, "userId": userID, "name": fullName})
}

// POST /api/agent/heartbeat — keeps the person online; tells the agent what to do
func (s *Server) agentHeartbeat(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r)
	var body struct {
		Capabilities *capability.Report `json:"capabilities"`
	}
	// Older agents send an empty body. Keep their existing capability value.
	err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8192)).Decode(&body)
	if err != nil && !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "Invalid capabilities")
		return
	}
	if body.Capabilities != nil {
		if _, err := s.db.Exec(r.Context(), `UPDATE devices SET capabilities = $2 WHERE id = $1`, d.ID, capability.JSON(body.Capabilities)); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	if _, err := s.db.Exec(r.Context(), `UPDATE users SET last_active_at = now() WHERE id = $1`, d.UserID); err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"live":            s.live.watched(d.UserID),
		"screenshotEvery": screenshotEverySec,
	})
}

type agentEvent struct {
	StartedAt time.Time `json:"startedAt"`
	Seconds   int       `json:"seconds"`
	App       string    `json:"app"`     // process name
	AppName   string    `json:"appName"` // friendly name
	Title     string    `json:"title"`
	Domain    string    `json:"domain"`
}

// POST /api/agent/activity — body {events: [...]}, up to 500 at a time.
// Each row is rated by the matching productivity rule; unseen apps get a neutral rule.
func (s *Server) agentActivity(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Events []agentEvent `json:"events"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Events) > 500 {
		writeError(w, http.StatusBadRequest, "Send at most 500 events at a time")
		return
	}
	d := deviceFrom(r)
	now := time.Now()
	rows := [][]any{}
	for _, e := range body.Events {
		if e.Seconds <= 0 || e.Seconds > 24*3600 || e.StartedAt.IsZero() || e.StartedAt.After(now.Add(5*time.Minute)) {
			continue
		}
		app := truncate(strings.TrimSpace(e.App), 200)
		if app == "" {
			continue
		}
		appName := truncate(strings.TrimSpace(e.AppName), 200)
		if appName == "" {
			appName = app
		}
		var domain any
		if dom := cleanDomain(e.Domain); dom != "" && domainPattern.MatchString(dom) {
			domain = dom
		}
		rows = append(rows, []any{d.UserID, d.ID, e.StartedAt, e.Seconds, app, appName, truncate(e.Title, 500), domain})
	}
	if len(rows) == 0 {
		writeJSON(w, http.StatusOK, map[string]int{"saved": 0})
		return
	}
	err := pgx.BeginFunc(r.Context(), s.db, func(tx pgx.Tx) error {
		// Copy into a scratch table, then insert with each row's rule applied
		if _, err := tx.Exec(r.Context(), `
			CREATE TEMP TABLE incoming (user_id bigint, device_id bigint, started_at timestamptz, duration_sec int,
				app text, app_name text, title text, domain text) ON COMMIT DROP`); err != nil {
			return err
		}
		if _, err := tx.CopyFrom(r.Context(), pgx.Identifier{"incoming"},
			[]string{"user_id", "device_id", "started_at", "duration_sec", "app", "app_name", "title", "domain"},
			pgx.CopyFromRows(rows)); err != nil {
			return err
		}
		if _, err := tx.Exec(r.Context(), `
			INSERT INTO activity_rules (kind, name, category, productivity)
			SELECT DISTINCT 'website'::activity_rule_kind, lower(domain), 'Uncategorized', 'neutral'::activity_productivity
			FROM incoming WHERE domain IS NOT NULL
			UNION ALL
			SELECT DISTINCT ON (lower(app_name)) 'application', app_name, 'Uncategorized', 'neutral'
			FROM incoming
			ON CONFLICT DO NOTHING`); err != nil {
			return err
		}
		_, err := tx.Exec(r.Context(), `
			INSERT INTO activity_events (user_id, device_id, started_at, duration_sec, app, app_name, title, domain, category, productivity)
			SELECT i.user_id, i.device_id, i.started_at, i.duration_sec, i.app, i.app_name, i.title, i.domain,
			       COALESCE(w.category, a.category, 'Uncategorized'), COALESCE(w.productivity, a.productivity, 'neutral')
			FROM incoming i
			LEFT JOIN activity_rules w ON w.kind = 'website' AND i.domain IS NOT NULL AND lower(w.name) = lower(i.domain)
			LEFT JOIN activity_rules a ON a.kind = 'application' AND lower(a.name) = lower(i.app_name)`)
		return err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]int{"saved": len(rows)})
}

// POST /api/agent/screenshots — multipart: file (JPEG or PNG), takenAt (RFC 3339), app, title
func (s *Server) agentScreenshot(w http.ResponseWriter, r *http.Request) {
	s.saveCapture(w, r, "screenshot", maxScreenshotBytes)
}

// POST /api/agent/recordings — multipart: file (WebM), startedAt (RFC 3339), seconds, app, title
func (s *Server) agentRecording(w http.ResponseWriter, r *http.Request) {
	// A recording piece can take longer to upload than the server's default read timeout
	http.NewResponseController(w).SetReadDeadline(time.Now().Add(recordingUploadMax))
	s.saveCapture(w, r, "recording", maxRecordingBytes)
}

var captureTypes = map[string]map[string]string{
	"screenshot": {"image/jpeg": ".jpg", "image/png": ".png"},
	"recording":  {"video/webm": ".webm"},
}

func (s *Server) saveCapture(w http.ResponseWriter, r *http.Request, kind string, limit int64) {
	d := deviceFrom(r)
	r.Body = http.MaxBytesReader(w, r.Body, limit+1<<20)
	if err := r.ParseMultipartForm(8 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "Send the file as multipart form data, within the size limit")
		return
	}
	defer r.MultipartForm.RemoveAll()

	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()
	ctype := header.Header.Get("Content-Type")
	ext, ok := captureTypes[kind][ctype]
	if !ok {
		writeError(w, http.StatusBadRequest, "Unsupported file type "+ctype)
		return
	}

	timeField := "takenAt"
	if kind == "recording" {
		timeField = "startedAt"
	}
	at, err := time.Parse(time.RFC3339, r.FormValue(timeField))
	if err != nil {
		writeError(w, http.StatusBadRequest, timeField+" must be an RFC 3339 time")
		return
	}
	seconds := 0
	if kind == "recording" {
		if seconds, err = strconv.Atoi(r.FormValue("seconds")); err != nil || seconds <= 0 || seconds > 4*3600 {
			writeError(w, http.StatusBadRequest, "seconds must be the recording length")
			return
		}
	}
	// A file cannot have been captured after it arrived. A time in the future means the computer's
	// clock was wrong (e.g. right after boot, before it synced), so use the arrival time instead.
	if now := time.Now(); at.After(now.Add(maxClockAhead)) {
		at = now.Add(-time.Duration(seconds) * time.Second)
	}

	// data/captures/<user>/<YYYY-MM-DD>/<kind>-<time>-<random><ext>
	suffix, _, err := auth.NewToken()
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rel := filepath.Join("captures", strconv.FormatInt(d.UserID, 10), at.UTC().Format(time.DateOnly),
		fmt.Sprintf("%s-%s-%s%s", kind, at.UTC().Format("150405"), suffix[:10], ext))
	full := filepath.Join(s.cfg.DataDir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		s.fail(w, r, err)
		return
	}
	out, err := os.OpenFile(full, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o640)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	n, err := io.Copy(out, io.LimitReader(file, limit+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil || n > limit || n == 0 {
		os.Remove(full)
		if err != nil {
			s.fail(w, r, err)
		} else {
			writeError(w, http.StatusBadRequest, "The file is empty or too large")
		}
		return
	}

	var id int64
	err = s.db.QueryRow(r.Context(), `
		INSERT INTO captures (user_id, device_id, kind, started_at, duration_sec, path, bytes, content_type, app, title)
		VALUES ($1, $2, $3::capture_kind, $4, $5, $6, $7, $8, $9, $10) RETURNING id`,
		d.UserID, d.ID, kind, at, seconds, filepath.ToSlash(rel), n, ctype,
		truncate(r.FormValue("app"), 200), truncate(r.FormValue("title"), 500)).Scan(&id)
	if err != nil {
		os.Remove(full)
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]int64{"id": id})
}

// POST /api/agent/live — body is one JPEG frame; kept in memory only, and only while someone watches
func (s *Server) agentLiveFrame(w http.ResponseWriter, r *http.Request) {
	d := deviceFrom(r)
	if !s.live.watched(d.UserID) {
		writeJSON(w, http.StatusOK, map[string]bool{"live": false})
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxLiveFrameBytes))
	if err != nil || len(data) == 0 {
		writeError(w, http.StatusBadRequest, "Send one JPEG frame up to 3 MB")
		return
	}
	s.live.put(d.UserID, data)
	writeJSON(w, http.StatusOK, map[string]bool{"live": true})
}

// ---------- Live view state (memory only) ----------

type liveFrame struct {
	data []byte
	at   time.Time
}

type liveHub struct {
	mu       sync.Mutex
	watching map[int64]time.Time // person -> last time a viewer asked
	frames   map[int64]liveFrame
}

func newLiveHub() *liveHub {
	return &liveHub{watching: map[int64]time.Time{}, frames: map[int64]liveFrame{}}
}

func (h *liveHub) watch(userID int64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.watching[userID] = time.Now()
}

func (h *liveHub) watched(userID int64) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return time.Since(h.watching[userID]) < liveWatchWindow
}

func (h *liveHub) put(userID int64, data []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.frames[userID] = liveFrame{data: data, at: time.Now()}
}

// frame returns the latest frame if it is recent enough to count as live.
func (h *liveHub) frame(userID int64) (liveFrame, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f, ok := h.frames[userID]
	if !ok || time.Since(f.at) > liveFrameMaxAge {
		return liveFrame{}, false
	}
	return f, true
}

// prune forgets viewers and frames nobody needs any more.
func (h *liveHub) prune() {
	h.mu.Lock()
	defer h.mu.Unlock()
	for id, at := range h.watching {
		if time.Since(at) > liveWatchWindow {
			delete(h.watching, id)
		}
	}
	for id, f := range h.frames {
		if time.Since(f.at) > liveFrameMaxAge {
			delete(h.frames, id)
		}
	}
}

// ---------- Retention ----------

// RunMaintenance deletes captures older than RETENTION_DAYS (files and rows) every hour,
// and clears stale live frames every minute, until ctx ends.
func (s *Server) RunMaintenance(ctx context.Context) {
	cleanup := time.NewTicker(time.Hour)
	prune := time.NewTicker(time.Minute)
	defer cleanup.Stop()
	defer prune.Stop()
	s.deleteOldCaptures(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-prune.C:
			s.live.prune()
		case <-cleanup.C:
			s.deleteOldCaptures(ctx)
		}
	}
}

func (s *Server) deleteOldCaptures(ctx context.Context) {
	if s.cfg.RetentionDays <= 0 {
		return
	}
	rows, err := s.db.Query(ctx, `
		DELETE FROM captures WHERE started_at < now() - make_interval(days => $1) RETURNING path`, s.cfg.RetentionDays)
	if err != nil {
		s.log.Error("retention", "error", err)
		return
	}
	paths, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		s.log.Error("retention", "error", err)
		return
	}
	for _, p := range paths {
		if err := os.Remove(filepath.Join(s.cfg.DataDir, filepath.FromSlash(p))); err != nil && !errors.Is(err, os.ErrNotExist) {
			s.log.Warn("retention: could not delete file", "path", p, "error", err)
		}
	}
	if len(paths) > 0 {
		s.log.Info("retention: deleted old captures", "count", len(paths), "days", s.cfg.RetentionDays)
	}
}

// ---------- Input counts (Activity Levels) ----------

type inputMinute struct {
	Minute time.Time `json:"minute"`
	Keys   int       `json:"keys"`
	Clicks int       `json:"clicks"`
}

// POST /api/agent/input — body {minutes: [{minute, keys, clicks}]}, up to 1440 at a time.
// Counts for a minute already sent are added to.
func (s *Server) agentInput(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Minutes []inputMinute `json:"minutes"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Minutes) > 1440 {
		writeError(w, http.StatusBadRequest, "Send at most 1440 minutes at a time")
		return
	}
	d := deviceFrom(r)
	batch := &pgx.Batch{}
	now := time.Now()
	for _, m := range body.Minutes {
		// A person cannot press 2000 keys or click 1000 times in a minute: drop nonsense
		if m.Minute.IsZero() || m.Minute.After(now.Add(5*time.Minute)) || m.Keys < 0 || m.Clicks < 0 || m.Keys > 2000 || m.Clicks > 1000 {
			continue
		}
		batch.Queue(`
			INSERT INTO input_counts (user_id, device_id, minute, keys, clicks) VALUES ($1, $2, date_trunc('minute', $3::timestamptz), $4, $5)
			ON CONFLICT (user_id, device_id, minute) DO UPDATE
			SET keys = input_counts.keys + EXCLUDED.keys, clicks = input_counts.clicks + EXCLUDED.clicks`,
			d.UserID, d.ID, m.Minute, m.Keys, m.Clicks)
	}
	if batch.Len() > 0 {
		if err := s.db.SendBatch(r.Context(), batch).Close(); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"saved": batch.Len()})
}

// ---------- Presence: sign-in, lock, unlock, sign-out ----------

type presenceEvent struct {
	Kind string    `json:"kind"` // login | unlock | lock | logout
	At   time.Time `json:"at"`
}

// POST /api/agent/presence — body {events: [{kind, at}]}, up to 500; repeats are ignored
func (s *Server) agentPresence(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Events []presenceEvent `json:"events"`
	}
	if !decode(w, r, &body) {
		return
	}
	if len(body.Events) > 500 {
		writeError(w, http.StatusBadRequest, "Send at most 500 events at a time")
		return
	}
	d := deviceFrom(r)
	batch := &pgx.Batch{}
	now := time.Now()
	for _, e := range body.Events {
		switch e.Kind {
		case "login", "unlock", "lock", "logout":
		default:
			continue
		}
		// A session can be weeks old; anything from the future is a broken clock
		if e.At.IsZero() || e.At.After(now.Add(5*time.Minute)) || e.At.Before(now.AddDate(0, -3, 0)) {
			continue
		}
		batch.Queue(`INSERT INTO presence_events (user_id, device_id, kind, at) VALUES ($1, $2, $3::presence_kind, $4)
			ON CONFLICT DO NOTHING`, d.UserID, d.ID, e.Kind, e.At.Truncate(time.Second))
	}
	if batch.Len() > 0 {
		if err := s.db.SendBatch(r.Context(), batch).Close(); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	writeJSON(w, http.StatusOK, map[string]int{"saved": batch.Len()})
}
