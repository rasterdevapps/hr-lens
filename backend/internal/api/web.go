package api

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

// spaHandler serves the built Angular app from dir. A request for a real file (JS, CSS, image)
// returns that file; any other path returns index.html so the browser-side router can handle it
// (e.g. /dashboard reloaded directly). Unknown /api, /extension and /healthz paths are left as
// 404s rather than being answered with the app shell.
func spaHandler(dir string) http.Handler {
	index := filepath.Join(dir, "index.html")
	fileServer := http.FileServer(http.Dir(dir))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if isServerPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		// Clean the request path and keep it inside dir
		clean := filepath.Clean("/" + r.URL.Path)
		full := filepath.Join(dir, filepath.FromSlash(clean))
		if !strings.HasPrefix(full, filepath.Clean(dir)+string(os.PathSeparator)) && full != filepath.Clean(dir) {
			http.NotFound(w, r)
			return
		}
		if info, err := os.Stat(full); err == nil && !info.IsDir() {
			fileServer.ServeHTTP(w, r)
			return
		}
		http.ServeFile(w, r, index) // client-side route: hand back the app shell
	})
}

// isServerPath reports whether a path is handled by the API/back end rather than the app shell.
func isServerPath(p string) bool {
	return strings.HasPrefix(p, "/api/") || strings.HasPrefix(p, "/extension/") || p == "/healthz"
}
