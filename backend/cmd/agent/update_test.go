package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNewer(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"0.2.0", "0.1.0", true}, {"0.10.0", "0.9.9", true}, {"1.0.0", "0.99.99", true},
		{"0.1.0", "0.1.0", false}, {"0.1.0", "0.2.0", false},
		{"0.2.0", "dev", false}, {"dev", "0.1.0", false}, {"0.2", "0.1.0", false}, {"0.2.x", "0.1.0", false},
	} {
		if got := newer(c.a, c.b); got != c.want {
			t.Errorf("newer(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// fakeRelease serves one release; tamper changes the program after it was signed.
func fakeRelease(t *testing.T, priv ed25519.PrivateKey, ver string, program []byte, tamper bool) *httptest.Server {
	sum := sha256.Sum256(program)
	sha := hex.EncodeToString(sum[:])
	sig := ed25519.Sign(priv, signedMessage(platform(), ver, sha))
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" || r.URL.Query().Get("platform") != platform() {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case "/api/agent/update":
			json.NewEncoder(w).Encode(release{Version: ver, SHA256: sha, Signature: base64.StdEncoding.EncodeToString(sig)})
		case "/api/agent/update/download":
			if tamper {
				w.Write([]byte("something else"))
				return
			}
			w.Write(program)
		}
	}))
}

func testAgent(server string) *agent {
	return &agent{cfg: config{Server: server, Token: "tok"}, log: slog.New(slog.NewTextHandler(io.Discard, nil)), http: http.DefaultClient}
}

func TestUpdate(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(nil)
	_, otherPriv, _ := ed25519.GenerateKey(nil)
	old := version
	version = "0.1.0"
	defer func() { version = old }()

	for _, c := range []struct {
		name, ver string
		key       ed25519.PrivateKey
		tamper    bool
		installed bool
		wantErr   string
	}{
		{name: "newer signed release", ver: "0.2.0", key: priv, installed: true},
		{name: "same version", ver: "0.1.0", key: priv},
		{name: "signed with another key", ver: "0.2.0", key: otherPriv, wantErr: "not signed"},
		{name: "program changed after signing", ver: "0.2.0", key: priv, tamper: true, wantErr: "does not match"},
	} {
		t.Run(c.name, func(t *testing.T) {
			bin := filepath.Join(t.TempDir(), "hrlens-agent")
			os.WriteFile(bin, []byte("old program"), 0o755)
			srv := fakeRelease(t, c.key, c.ver, []byte("new program"), c.tamper)
			defer srv.Close()

			got, err := testAgent(srv.URL).update(context.Background(), pub, bin)
			if c.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), c.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, c.wantErr)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			data, _ := os.ReadFile(bin)
			if c.installed {
				if got != c.ver || string(data) != "new program" {
					t.Fatalf("got version %q and program %q, want %s installed", got, data, c.ver)
				}
				info, err := os.Stat(bin)
				if err != nil {
					t.Fatal(err)
				}
				// Windows represents read/write attributes, not POSIX executable bits.
				if runtime.GOOS != "windows" && info.Mode().Perm()&0o100 == 0 {
					t.Fatal("updated program is not executable")
				}
			} else if got != "" || string(data) != "old program" {
				t.Fatalf("got version %q and program %q, want the old program kept", got, data)
			}
			if _, err := os.Stat(bin + ".download"); !os.IsNotExist(err) {
				t.Fatal("download left behind")
			}
		})
	}
}

func TestUpload(t *testing.T) {
	t.Run("streams multipart and cleans up after acceptance", func(t *testing.T) {
		dir := t.TempDir()
		shot := filepath.Join(dir, "screenshot-20261021T100000.jpg")
		wantBytes := []byte("fake-jpeg-payload")
		if err := os.WriteFile(shot, wantBytes, 0o600); err != nil {
			t.Fatal(err)
		}
		writeMeta(shot, map[string]string{"takenAt": "2026-10-21T10:00:00Z", "app": "Editor", "title": "main.go"})

		var gotApp, gotTitle, gotTakenAt string
		var gotFile []byte
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/agent/screenshots" || r.Header.Get("Authorization") != "Bearer tok" {
				http.Error(w, "bad request", http.StatusBadRequest)
				return
			}
			if err := r.ParseMultipartForm(1 << 20); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			gotApp = r.FormValue("app")
			gotTitle = r.FormValue("title")
			gotTakenAt = r.FormValue("takenAt")
			f, _, err := r.FormFile("file")
			if err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			defer f.Close()
			gotFile, _ = io.ReadAll(f)
			w.WriteHeader(http.StatusCreated)
		}))
		defer srv.Close()

		if err := testAgent(srv.URL).upload(context.Background(), shot); err != nil {
			t.Fatalf("upload failed: %v", err)
		}
		if gotApp != "Editor" || gotTitle != "main.go" || gotTakenAt != "2026-10-21T10:00:00Z" {
			t.Fatalf("got meta (%q, %q, %q)", gotApp, gotTitle, gotTakenAt)
		}
		if !bytes.Equal(gotFile, wantBytes) {
			t.Fatalf("got file %q, want %q", gotFile, wantBytes)
		}
		for _, p := range []string{shot, shot + ".json", shot + ".uploaded"} {
			if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected %s to be removed, stat err = %v", p, err)
			}
		}
	})

	t.Run("unblocks pipe on early rejection without reading body", func(t *testing.T) {
		dir := t.TempDir()
		rec := filepath.Join(dir, "recording-20261021T100000.webm")
		largePayload := bytes.Repeat([]byte("w"), 512<<10)
		if err := os.WriteFile(rec, largePayload, 0o600); err != nil {
			t.Fatal(err)
		}
		writeMeta(rec, map[string]string{"startedAt": "2026-10-21T10:00:00Z", "seconds": "60", "app": "Browser"})

		status := http.StatusInternalServerError
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(status)
			w.Write([]byte("rejected early"))
		}))
		defer srv.Close()

		a := testAgent(srv.URL)
		if err := a.upload(context.Background(), rec); err == nil {
			t.Fatal("expected error on 500 response")
		}
		if _, err := os.Stat(rec); err != nil {
			t.Fatalf("expected recording to be kept after 500: %v", err)
		}
		if _, err := os.Stat(rec + ".json"); err != nil {
			t.Fatalf("expected metadata to be kept after 500: %v", err)
		}
		if _, err := os.Stat(rec + ".uploaded"); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("unexpected .uploaded receipt after 500: %v", err)
		}

		// A 400 Bad Request drops the file permanently even if the server did not drain the body.
		status = http.StatusBadRequest
		if err := a.upload(context.Background(), rec); err != nil {
			t.Fatalf("expected nil on 400 drop, got %v", err)
		}
		for _, p := range []string{rec, rec + ".json", rec + ".uploaded"} {
			if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected %s to be removed on 400, stat err = %v", p, err)
			}
		}
	})

	t.Run("durable uploaded receipt retries only cleanup and preserves metadata until cleanup succeeds", func(t *testing.T) {
		dir := t.TempDir()
		shot := filepath.Join(dir, "screenshot-20261021T110000.jpg")
		if err := os.WriteFile(shot, []byte("jpeg-data"), 0o600); err != nil {
			t.Fatal(err)
		}
		writeMeta(shot, map[string]string{"takenAt": "2026-10-21T11:00:00Z", "app": "Terminal", "title": "bash"})

		lockFile := filepath.Join(shot+".json", "lock")
		var uploads atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			uploads.Add(1)
			_, _ = io.Copy(io.Discard, r.Body)
			// Metadata is closed before HTTP starts, so this simulates a cleanup
			// failure on every OS without trying to delete an open Windows file.
			_ = os.Remove(shot + ".json")
			_ = os.MkdirAll(shot+".json", 0o700)
			_ = os.WriteFile(lockFile, []byte("locked"), 0o600)
			w.WriteHeader(http.StatusCreated)
		}))
		defer srv.Close()

		a := testAgent(srv.URL)
		if err := a.upload(context.Background(), shot); err == nil {
			t.Fatal("expected cleanup failure on first upload call")
		}
		if got := uploads.Load(); got != 1 {
			t.Fatalf("uploads = %d, want 1", got)
		}
		if _, err := os.Stat(shot + ".uploaded"); err != nil {
			t.Fatalf("expected durable .uploaded receipt to exist: %v", err)
		}
		if _, err := os.Stat(shot + ".json"); err != nil {
			t.Fatalf("expected metadata .json to be preserved while cleanup fails: %v", err)
		}

		// Clear the lock and retry: must only clean up and never resend to the server.
		if err := os.Remove(lockFile); err != nil {
			t.Fatal(err)
		}
		if err := a.upload(context.Background(), shot); err != nil {
			t.Fatalf("retry cleanup failed: %v", err)
		}
		if got := uploads.Load(); got != 1 {
			t.Fatalf("server received %d uploads, want 1 (must not resend)", got)
		}
		for _, p := range []string{shot, shot + ".json", shot + ".uploaded"} {
			if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("expected %s to be removed after retry cleanup, stat err = %v", p, err)
			}
		}
	})
}

func TestReplaceWithBackup(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "hrlens-agentw.exe")
	if err := os.WriteFile(dst, []byte("v1"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Simulate a locked legacy .old path that cannot be removed.
	legacyOldLock := filepath.Join(dst+".old", "locked")
	if err := os.MkdirAll(filepath.Dir(legacyOldLock), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacyOldLock, []byte("locked"), 0o600); err != nil {
		t.Fatal(err)
	}

	lockedBackups := true
	removeFn := func(p string) error {
		if lockedBackups && strings.Contains(filepath.Base(p), ".old") {
			return os.ErrPermission
		}
		return os.Remove(p)
	}

	// First replacement: v1 -> v2
	src1 := filepath.Join(dir, "v2.new")
	if err := os.WriteFile(src1, []byte("v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceWithBackup(src1, dst, os.Rename, removeFn); err != nil {
		t.Fatalf("first replacement failed: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "v2" {
		t.Fatalf("after first replace dst = %q, want v2", got)
	}
	backups1, _ := filepath.Glob(dst + ".old.*")
	if len(backups1) != 1 {
		t.Fatalf("expected 1 unique backup after first replace, got %v", backups1)
	}
	if got, _ := os.ReadFile(backups1[0]); string(got) != "v1" {
		t.Fatalf("first backup content = %q, want v1", got)
	}

	// Second replacement while first backup and legacy .old are still locked: v2 -> v3
	src2 := filepath.Join(dir, "v3.new")
	if err := os.WriteFile(src2, []byte("v3"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := replaceWithBackup(src2, dst, os.Rename, removeFn); err != nil {
		t.Fatalf("second replacement with locked backup failed: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "v3" {
		t.Fatalf("after second replace dst = %q, want v3", got)
	}
	backups2, _ := filepath.Glob(dst + ".old.*")
	if len(backups2) != 2 {
		t.Fatalf("expected 2 unique backups while locked, got %v", backups2)
	}

	// Failed replacement rolls back to v3 when moving new file into place fails.
	srcFail := filepath.Join(dir, "v4.new")
	if err := os.WriteFile(srcFail, []byte("v4"), 0o755); err != nil {
		t.Fatal(err)
	}
	failSecondRename := func(oldpath, newpath string) error {
		if oldpath == srcFail && newpath == dst {
			return errors.New("simulated move failure")
		}
		return os.Rename(oldpath, newpath)
	}
	if err := replaceWithBackup(srcFail, dst, failSecondRename, removeFn); err == nil {
		t.Fatal("expected error when moving new file fails")
	}
	if got, err := os.ReadFile(dst); err != nil || string(got) != "v3" {
		t.Fatalf("expected rollback to restore v3, got %q (err=%v)", got, err)
	}
	backupsAfterRollback, _ := filepath.Glob(dst + ".old.*")
	if len(backupsAfterRollback) != 2 {
		t.Fatalf("expected rollback to consume its temporary backup, got %v", backupsAfterRollback)
	}

	// Once old backups are no longer locked, a subsequent replacement cleans them up.
	lockedBackups = false
	if err := replaceWithBackup(srcFail, dst, os.Rename, removeFn); err != nil {
		t.Fatalf("replacement after unlock failed: %v", err)
	}
	if got, _ := os.ReadFile(dst); string(got) != "v4" {
		t.Fatalf("after unlock replace dst = %q, want v4", got)
	}
	backupsFinal, _ := filepath.Glob(dst + ".old.*")
	if len(backupsFinal) != 1 {
		t.Fatalf("expected unlocked prior backups to be cleaned up leaving 1 current backup, got %v", backupsFinal)
	}
}
