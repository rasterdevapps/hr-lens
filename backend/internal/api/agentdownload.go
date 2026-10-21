package api

import (
	"net/http"
	"os"
	"path/filepath"
)

// Install packages an employee downloads from the "Download Agent" dialog, one per operating
// system. cmd/release writes the Linux and Windows ones (dist/hrlens-agent-linux.tar.gz,
// dist/hrlens-agent-windows.zip); macOS is not built yet, so it shows as unavailable.
var agentPackages = []struct {
	OS, Label, File, Ctype string
}{
	{"windows", "Windows", "hrlens-agent-windows.zip", "application/zip"},
	{"mac", "Mac", "hrlens-agent-macos.zip", "application/zip"},
	{"linux", "Linux", "hrlens-agent-linux.tar.gz", "application/gzip"},
}

// packagesDir is where the install packages sit: next to the per-platform release folders
// (AgentDir is dist/agent, so this is dist, where cmd/release writes the Linux tarball).
func (s *Server) packagesDir() string { return filepath.Dir(s.cfg.AgentDir) }

type downloadInfo struct {
	OS        string `json:"os"`
	Label     string `json:"label"`
	Available bool   `json:"available"`
}

// GET /api/agent/downloads — which operating systems have an install package ready.
func (s *Server) agentDownloads(w http.ResponseWriter, r *http.Request) {
	out := make([]downloadInfo, 0, len(agentPackages))
	for _, p := range agentPackages {
		_, err := os.Stat(filepath.Join(s.packagesDir(), p.File))
		out = append(out, downloadInfo{OS: p.OS, Label: p.Label, Available: err == nil})
	}
	writeJSON(w, http.StatusOK, map[string]any{"platforms": out})
}

// GET /api/agent/download?os=linux — the install package for that operating system.
func (s *Server) agentDownload(w http.ResponseWriter, r *http.Request) {
	osName := r.URL.Query().Get("os")
	for _, p := range agentPackages {
		if p.OS != osName {
			continue
		}
		path := filepath.Join(s.packagesDir(), p.File)
		if _, err := os.Stat(path); err != nil {
			writeError(w, http.StatusNotFound, "The "+p.Label+" agent is not available yet")
			return
		}
		w.Header().Set("Content-Type", p.Ctype)
		w.Header().Set("Content-Disposition", `attachment; filename="`+p.File+`"`)
		http.ServeFile(w, r, path)
		return
	}
	writeError(w, http.StatusBadRequest, "Unknown operating system")
}
