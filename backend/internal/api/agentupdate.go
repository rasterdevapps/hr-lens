package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// ---------- Desktop agent updates ----------
//
// cmd/release builds the agent for each platform into AGENT_DIR/<os>-<arch>/ (the program and
// info.json with its version, SHA-256 and signature). Agents ask about once an hour and install
// a newer version only if the signature matches the public key built into them, so this server
// cannot hand them a program that was not signed with the release key.

var platformPattern = regexp.MustCompile(`^[a-z0-9]+-[a-z0-9]+$`)

type agentRelease struct {
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

func (s *Server) agentReleaseDir(r *http.Request) (string, bool) {
	platform := r.URL.Query().Get("platform")
	if !platformPattern.MatchString(platform) {
		return "", false
	}
	return filepath.Join(s.cfg.AgentDir, platform), true
}

// GET /api/agent/update?platform=linux-amd64 — the newest release, or 204 when none is published
func (s *Server) agentUpdate(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.agentReleaseDir(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "platform must look like linux-amd64")
		return
	}
	data, err := os.ReadFile(filepath.Join(dir, "info.json"))
	if errors.Is(err, os.ErrNotExist) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	var rel agentRelease
	if err == nil {
		err = json.Unmarshal(data, &rel)
	}
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, rel)
}

// GET /api/agent/update/download?platform=linux-amd64 — the program itself
func (s *Server) agentUpdateDownload(w http.ResponseWriter, r *http.Request) {
	dir, ok := s.agentReleaseDir(r)
	if !ok {
		writeError(w, http.StatusBadRequest, "platform must look like linux-amd64")
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeFile(w, r, filepath.Join(dir, "hrlens-agent"))
}

// agentVersion reads the version from the agent's User-Agent ("hrlens-agent/0.2.0").
func agentVersion(r *http.Request) string {
	v, ok := strings.CutPrefix(r.UserAgent(), "hrlens-agent/")
	if !ok || len(v) > 30 {
		return ""
	}
	return v
}
