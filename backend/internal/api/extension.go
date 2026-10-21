package api

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"os"
	"path/filepath"
)

// ---------- Browser extension downloads ----------
//
// Chromium-based browsers install the HR Lens extension through admin policy
// (ExtensionInstallForcelist "<id>;<server>/extension/updates.xml"), then fetch the
// package from here and check for new versions. No sign-in: browsers send no cookies.

type extensionInfo struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

func (s *Server) extensionInfo() (extensionInfo, error) {
	var info extensionInfo
	data, err := os.ReadFile(filepath.Join(s.cfg.ExtensionDir, "info.json"))
	if err != nil {
		return info, err
	}
	err = json.Unmarshal(data, &info)
	return info, err
}

// GET /extension/updates.xml — Chrome's update manifest, pointing at this server's package
func (s *Server) extensionUpdates(w http.ResponseWriter, r *http.Request) {
	info, err := s.extensionInfo()
	if err != nil {
		writeError(w, http.StatusNotFound, "The browser extension has not been built on this server")
		return
	}
	scheme := "http"
	if r.TLS != nil || s.cfg.CookieSecure {
		scheme = "https"
	}
	codebase := fmt.Sprintf("%s://%s/extension/hrlens.crx", scheme, r.Host)
	w.Header().Set("Content-Type", "application/xml; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	fmt.Fprintf(w, `<?xml version="1.0" encoding="UTF-8"?>
<gupdate xmlns="http://www.google.com/update2/response" protocol="2.0">
  <app appid="%s">
    <updatecheck codebase="%s" version="%s" />
  </app>
</gupdate>
`, html.EscapeString(info.ID), html.EscapeString(codebase), html.EscapeString(info.Version))
}

// GET /extension/hrlens.crx — the signed package
func (s *Server) extensionPackage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/x-chrome-extension")
	http.ServeFile(w, r, filepath.Join(s.cfg.ExtensionDir, "hrlens.crx"))
}
