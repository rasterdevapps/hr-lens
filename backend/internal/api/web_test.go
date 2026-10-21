package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestSPAHandler(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<app-root>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "main.js"), []byte("console.log(1)"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := spaHandler(dir)

	cases := []struct{ path, wantBody string }{
		{"/", "<app-root>"},            // root serves index
		{"/main.js", "console.log(1)"}, // real file served as-is
		{"/dashboard", "<app-root>"},   // client route falls back to index
		{"/users/42", "<app-root>"},    // nested client route too
	}
	for _, c := range cases {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, c.path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: status %d", c.path, rec.Code)
		}
		if got := rec.Body.String(); got != c.wantBody {
			t.Errorf("%s: body = %q, want %q", c.path, got, c.wantBody)
		}
	}

	// API/extension/health paths are not answered with the app shell
	for _, p := range []string{"/api/anything", "/extension/x", "/healthz"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, p, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s: status = %d, want 404", p, rec.Code)
		}
	}

	// Path traversal is refused
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/../secret", nil))
	if rec.Code == http.StatusOK && rec.Body.String() != "<app-root>" {
		t.Errorf("traversal leaked: %q", rec.Body.String())
	}
}
