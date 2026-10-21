package api

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

// ---------- Desktops: computers running the agent ----------

type deviceOwner struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
	Team  string `json:"team"`
}

type deviceRow struct {
	ID           int64       `json:"id"`
	Name         string      `json:"name"`
	OS           string      `json:"os"`       // as the agent reports it, e.g. linux/amd64
	Platform     string      `json:"platform"` // release folder name, e.g. linux-amd64
	AgentVersion string      `json:"agentVersion"`
	User         deviceOwner `json:"user"`
	CreatedAt    time.Time   `json:"createdAt"`
	LastSeenAt   *time.Time  `json:"lastSeenAt"`
	RevokedAt    *time.Time  `json:"revokedAt"`
	Online       bool        `json:"online"`
}

type deviceList struct {
	Latest map[string]string `json:"latest"` // newest published agent version per platform
	Items  []deviceRow       `json:"items"`
}

// GET /api/devices?status=active|removed
func (s *Server) listDevices(w http.ResponseWriter, r *http.Request) {
	status := cmp(r.URL.Query().Get("status"), "active")
	if status != "active" && status != "removed" {
		writeError(w, http.StatusBadRequest, "status must be active or removed")
		return
	}
	rows, err := s.db.Query(r.Context(), `
		SELECT d.id, d.name, d.os, d.agent_version, d.created_at, d.last_seen_at, d.revoked_at,
		       u.id, trim(u.first_name || ' ' || u.last_name), u.email, COALESCE(t.name, '')
		FROM devices d
		JOIN users u ON u.id = d.user_id
		LEFT JOIN teams t ON t.id = u.team_id
		WHERE (d.revoked_at IS NULL) = ($1 = 'active')
		ORDER BY d.last_seen_at DESC NULLS LAST, d.id DESC
		LIMIT 1000`, status)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	items, err := pgx.CollectRows(rows, func(row pgx.CollectableRow) (deviceRow, error) {
		var d deviceRow
		err := row.Scan(&d.ID, &d.Name, &d.OS, &d.AgentVersion, &d.CreatedAt, &d.LastSeenAt, &d.RevokedAt,
			&d.User.ID, &d.User.Name, &d.User.Email, &d.User.Team)
		d.Platform = strings.ReplaceAll(d.OS, "/", "-")
		d.Online = d.RevokedAt == nil && d.LastSeenAt != nil && time.Since(*d.LastSeenAt) < deviceOnlineWithin
		return d, err
	})
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if items == nil {
		items = []deviceRow{}
	}
	writeJSON(w, http.StatusOK, deviceList{Latest: s.latestAgentVersions(), Items: items})
}

// latestAgentVersions reads info.json of every published platform; a missing or broken one is skipped.
func (s *Server) latestAgentVersions() map[string]string {
	out := map[string]string{}
	dirs, err := os.ReadDir(s.cfg.AgentDir)
	if err != nil {
		return out
	}
	for _, d := range dirs {
		if !d.IsDir() || !platformPattern.MatchString(d.Name()) {
			continue
		}
		data, err := os.ReadFile(filepath.Join(s.cfg.AgentDir, d.Name(), "info.json"))
		var rel agentRelease
		if err == nil && json.Unmarshal(data, &rel) == nil && rel.Version != "" {
			out[d.Name()] = rel.Version
		}
	}
	return out
}

// DELETE /api/devices/{id} — removes a computer: its agent stops being accepted and must be registered again.
// Its screenshots, recordings and activity are kept.
func (s *Server) removeDevice(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r)
	if !ok {
		return
	}
	tag, err := s.db.Exec(r.Context(), `UPDATE devices SET revoked_at = now() WHERE id = $1 AND revoked_at IS NULL`, id)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if tag.RowsAffected() == 0 {
		writeError(w, http.StatusNotFound, "This computer was not found or is already removed")
		return
	}
	s.log.Info("device removed", "device", id, "by", userFrom(r).ID)
	w.WriteHeader(http.StatusNoContent)
}
