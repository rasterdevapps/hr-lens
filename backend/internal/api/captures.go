package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"hrlens/backend/internal/auth"
	"hrlens/backend/internal/capability"
)

// ---------- Screenshots and recordings for the Monitoring pages ----------

type captureJSON struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"userId"`
	UserName  string    `json:"userName"`
	Kind      string    `json:"kind"`
	StartedAt time.Time `json:"startedAt"`
	Seconds   int       `json:"seconds"`
	App       string    `json:"app"`
	Title     string    `json:"title"`
	Bytes     int64     `json:"bytes"`
	Device    string    `json:"device"`
}

// visibleCapture limits captures "c" to people the caller may see.
// Parameters: $1 role, $2 caller id, $3 user ids (empty = all visible).
const visibleCapture = `c.user_id IN (
	SELECT u.id FROM users u
	WHERE ` + visiblePeople + ` AND (cardinality($3::bigint[]) = 0 OR u.id = ANY($3::bigint[])))`

// GET /api/monitoring/captures?kind=screenshot|recording&users=&from=&to=&tz= — oldest first
func (s *Server) listCaptures(w http.ResponseWriter, r *http.Request) {
	kind := r.URL.Query().Get("kind")
	if kind != "screenshot" && kind != "recording" {
		writeError(w, http.StatusBadRequest, "kind must be screenshot or recording")
		return
	}
	me := userFrom(r)
	if kind != "screenshot" && me.Role == auth.User {
		writeError(w, http.StatusForbidden, "You don't have access to this")
		return
	}
	f, msg := parseActivityFilter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT c.id, c.user_id, trim(u.first_name || ' ' || u.last_name), c.kind::text, c.started_at, c.duration_sec,
		       c.app, c.title, c.bytes, COALESCE(d.name, '')
		FROM captures c JOIN users u ON u.id = c.user_id LEFT JOIN devices d ON d.id = c.device_id
		WHERE `+visibleCapture+`
		  AND c.kind = $4::capture_kind
		  AND c.started_at >= ($5::date)::timestamp AT TIME ZONE $7
		  AND c.started_at < ($6::date + 1)::timestamp AT TIME ZONE $7
		ORDER BY c.started_at, c.id
		LIMIT 5000`,
		string(me.Role), me.ID, f.users, kind, f.from.Format(time.DateOnly), f.to.Format(time.DateOnly), f.tz)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (captureJSON, error) {
		var c captureJSON
		return c, row.Scan(&c.ID, &c.UserID, &c.UserName, &c.Kind, &c.StartedAt, &c.Seconds, &c.App, &c.Title, &c.Bytes, &c.Device)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []captureJSON{}
	}
	writeJSON(w, http.StatusOK, list)
}

// GET /api/monitoring/captures/{id}/file — the image or video; supports Range for seeking in videos
func (s *Server) captureFile(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	me := userFrom(r)
	var path, ctype string
	var at time.Time
	err := s.db.QueryRow(r.Context(), `
		SELECT c.path, c.content_type, c.created_at FROM captures c
		WHERE c.id = $4 AND (c.kind = 'screenshot' OR $1 <> 'user') AND `+visibleCapture,
		string(me.Role), me.ID, []int64{}, id).Scan(&path, &ctype, &at)
	if err != nil {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	file, err := os.Open(filepath.Join(s.cfg.DataDir, filepath.FromSlash(path)))
	if err != nil {
		writeError(w, http.StatusNotFound, "The file is no longer stored")
		return
	}
	defer file.Close()
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("Cache-Control", "private, max-age=86400") // a capture never changes
	h.Set("X-Content-Type-Options", "nosniff")
	// Long videos may take longer than the server's default write timeout
	http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
	http.ServeContent(w, r, filepath.Base(path), at, file)
}

type liveStatus struct {
	UserID   int64      `json:"userId"`
	Online   bool       `json:"online"` // a device sent a heartbeat recently
	Device   string     `json:"device"` // most recently seen device
	LastSeen *time.Time `json:"lastSeen"`
	FrameAt  *time.Time `json:"frameAt"` // newest live frame, if any
	// NoScreen reflects this device's feature report, with a legacy Linux fallback.
	NoScreen     bool            `json:"noScreen"`
	Capabilities json.RawMessage `json:"capabilities"`
}

// GET /api/monitoring/live?users=1,2 — who can be watched; asking also starts their live view
func (s *Server) liveStatus(w http.ResponseWriter, r *http.Request) {
	f, msg := parseUsersOnly(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	me := userFrom(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT u.id, d.name, d.os, d.last_seen_at, d.capabilities
		FROM users u
		LEFT JOIN LATERAL (
			SELECT name, os, last_seen_at, capabilities FROM devices
			WHERE user_id = u.id AND revoked_at IS NULL
			ORDER BY last_seen_at DESC NULLS LAST LIMIT 1
		) d ON true
		WHERE u.status = 'active' AND `+visiblePeople+` AND (cardinality($3::bigint[]) = 0 OR u.id = ANY($3::bigint[]))
		ORDER BY u.id
		LIMIT 100`, string(me.Role), me.ID, f)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (liveStatus, error) {
		var st liveStatus
		var name, osName *string
		err := row.Scan(&st.UserID, &name, &osName, &st.LastSeen, &st.Capabilities)
		if name != nil {
			st.Device = *name
		}
		st.NoScreen = osName == nil || !capability.Live(*osName, st.Capabilities)
		return st, err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	for i := range list {
		st := &list[i]
		st.Online = st.LastSeen != nil && time.Since(*st.LastSeen) < deviceOnlineWithin
		if st.Online && !st.NoScreen {
			s.live.watch(st.UserID)
		}
		if fr, ok := s.live.frame(st.UserID); ok {
			at := fr.at
			st.FrameAt = &at
		}
	}
	if list == nil {
		list = []liveStatus{}
	}
	writeJSON(w, http.StatusOK, list)
}

// GET /api/monitoring/live/{id}/frame — the newest live frame of one person (JPEG)
func (s *Server) liveFrame(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	me := userFrom(r)
	var visible bool
	if err := s.db.QueryRow(r.Context(), `SELECT EXISTS (SELECT 1 FROM users u WHERE u.id = $3 AND `+visiblePeople+`)`,
		string(me.Role), me.ID, id).Scan(&visible); err != nil || !visible {
		writeError(w, http.StatusNotFound, "Not found")
		return
	}
	var osName string
	var caps []byte
	err := s.db.QueryRow(r.Context(), `SELECT os, capabilities FROM devices WHERE user_id = $1 AND revoked_at IS NULL ORDER BY last_seen_at DESC NULLS LAST LIMIT 1`, id).Scan(&osName, &caps)
	if err != nil || !capability.Live(osName, caps) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	s.live.watch(id)
	fr, ok := s.live.frame(id)
	if !ok {
		w.WriteHeader(http.StatusNoContent) // waiting for the agent's first frame
		return
	}
	h := w.Header()
	h.Set("Content-Type", "image/jpeg")
	h.Set("Cache-Control", "no-store")
	h.Set("X-Frame-Time", fr.at.UTC().Format(time.RFC3339Nano))
	w.Write(fr.data)
}

// parseUsersOnly reads users=1,2,3.
func parseUsersOnly(r *http.Request) ([]int64, string) {
	out := []int64{}
	raw := r.URL.Query().Get("users")
	if raw == "" {
		return out, ""
	}
	for _, part := range strings.Split(raw, ",") {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			return nil, "users must be a list of user ids"
		}
		out = append(out, id)
	}
	return out, ""
}

type inputBucket struct {
	Start  int `json:"start"` // seconds after local midnight of the day
	Keys   int `json:"keys"`
	Clicks int `json:"clicks"`
}

// GET /api/monitoring/input?users=&from=&to=&tz= — key presses and clicks in 10-minute buckets
// (summed over the people and days asked for)
func (s *Server) inputLevels(w http.ResponseWriter, r *http.Request) {
	f, msg := parseActivityFilter(r)
	if msg != "" {
		writeError(w, http.StatusBadRequest, msg)
		return
	}
	me := userFrom(r)
	rows, err := s.db.Query(r.Context(), `
		SELECT (extract(epoch FROM (c.minute AT TIME ZONE $6) - (c.minute AT TIME ZONE $6)::date)::int / 600) * 600 AS start,
		       sum(c.keys)::int, sum(c.clicks)::int
		FROM input_counts c
		WHERE c.user_id IN (
			SELECT u.id FROM users u
			WHERE `+visiblePeople+` AND (cardinality($3::bigint[]) = 0 OR u.id = ANY($3::bigint[])))
		  AND c.minute >= ($4::date)::timestamp AT TIME ZONE $6
		  AND c.minute < ($5::date + 1)::timestamp AT TIME ZONE $6
		GROUP BY 1 ORDER BY 1`,
		string(me.Role), me.ID, f.users, f.from.Format(time.DateOnly), f.to.Format(time.DateOnly), f.tz)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	list, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (inputBucket, error) {
		var b inputBucket
		return b, row.Scan(&b.Start, &b.Keys, &b.Clicks)
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if list == nil {
		list = []inputBucket{}
	}
	writeJSON(w, http.StatusOK, list)
}
