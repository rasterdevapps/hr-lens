package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
