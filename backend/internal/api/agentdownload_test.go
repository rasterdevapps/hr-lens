package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"hrlens/backend/internal/config"
)

// newDownloadServer makes a Server whose packages dir (parent of AgentDir) holds only the files
// named, so the handlers see exactly those as available.
func newDownloadServer(t *testing.T, files map[string][]byte) *Server {
	t.Helper()
	dist := t.TempDir()
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(dist, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Server{cfg: config.Config{AgentDir: filepath.Join(dist, "agent")}}
}

func TestAgentDownloadsReportsAvailability(t *testing.T) {
	s := newDownloadServer(t, map[string][]byte{"hrlens-agent-linux.tar.gz": []byte("pkg")})

	rec := httptest.NewRecorder()
	s.agentDownloads(rec, httptest.NewRequest(http.MethodGet, "/api/agent/downloads", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	var body struct {
		Platforms []downloadInfo `json:"platforms"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, p := range body.Platforms {
		got[p.OS] = p.Available
	}
	if !got["linux"] {
		t.Error("linux should be available")
	}
	if got["windows"] || got["mac"] {
		t.Error("windows and mac should be unavailable")
	}
}

func TestAgentDownloadServesPackage(t *testing.T) {
	s := newDownloadServer(t, map[string][]byte{"hrlens-agent-linux.tar.gz": []byte("the-package-bytes")})

	rec := httptest.NewRecorder()
	s.agentDownload(rec, httptest.NewRequest(http.MethodGet, "/api/agent/download?os=linux", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if rec.Body.String() != "the-package-bytes" {
		t.Errorf("body = %q, want the package bytes", rec.Body.String())
	}
	if cd := rec.Header().Get("Content-Disposition"); cd != `attachment; filename="hrlens-agent-linux.tar.gz"` {
		t.Errorf("Content-Disposition = %q", cd)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/gzip" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestAgentDownloadMissingAndUnknown(t *testing.T) {
	s := newDownloadServer(t, nil) // nothing built

	rec := httptest.NewRecorder()
	s.agentDownload(rec, httptest.NewRequest(http.MethodGet, "/api/agent/download?os=windows", nil))
	if rec.Code != http.StatusNotFound {
		t.Errorf("missing package status = %d, want 404", rec.Code)
	}

	rec = httptest.NewRecorder()
	s.agentDownload(rec, httptest.NewRequest(http.MethodGet, "/api/agent/download?os=bsd", nil))
	if rec.Code != http.StatusBadRequest {
		t.Errorf("unknown os status = %d, want 400", rec.Code)
	}
}
