package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Self-update: about once an hour the agent asks the server for the newest release of its
// platform. A newer one is downloaded next to the installed program, checked against the
// release public key built into this program (updatekey.go), and swapped in; then the agent
// stops and supervise starts the new version from the same path.

const (
	updateEvery    = time.Hour
	firstUpdate    = time.Minute // after starting, so sign-in is not slowed down
	maxUpdateBytes = 200 << 20
)

type release struct {
	Version   string `json:"version"`
	SHA256    string `json:"sha256"`
	Signature string `json:"signature"`
}

func platform() string { return runtime.GOOS + "-" + runtime.GOARCH }

// signedMessage is what cmd/release signs: the platform and version are included so a
// signed program cannot be passed off as another version or another platform's.
func signedMessage(platform, version, sha string) []byte {
	return []byte(fmt.Sprintf("hrlens-agent %s %s %s", platform, version, sha))
}

func (a *agent) updates(ctx context.Context) {
	if version == "dev" {
		return // a developer build: never replace it
	}
	key, err := base64.StdEncoding.DecodeString(updateKey)
	if err != nil || len(key) != ed25519.PublicKeySize {
		a.log.Warn("automatic updates are off: this build has no release key")
		return
	}
	bin, _, err := autostartPaths()
	if err != nil {
		return
	}
	if self, err := os.Executable(); err != nil || !samePath(self, bin) {
		a.log.Info("automatic updates are off: only the installed copy updates itself", "installed", bin)
		return
	}
	sleep(ctx, firstUpdate)
	if ctx.Err() != nil {
		return
	}
	every(ctx, updateEvery, func() {
		updated, err := a.update(ctx, ed25519.PublicKey(key), bin)
		switch {
		case err != nil:
			a.log.Warn("update check failed; will try again later", "error", err)
		case updated != "":
			a.log.Info("updated; restarting", "from", version, "to", updated)
			a.restart()
		}
	})
}

// update installs a newer signed release over bin and returns its version, or "" if there is none.
func (a *agent) update(ctx context.Context, key ed25519.PublicKey, bin string) (string, error) {
	query := "?platform=" + platform()
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.Server+"/api/agent/update"+query, nil)
	resp, err := a.do(req)
	if err != nil {
		return "", err
	}
	var rel release
	switch resp.StatusCode {
	case http.StatusOK:
		err = json.NewDecoder(resp.Body).Decode(&rel)
	case http.StatusNoContent:
	default:
		err = fmt.Errorf("server answered %s", resp.Status)
	}
	resp.Body.Close()
	if err != nil || !newer(rel.Version, version) {
		return "", err
	}

	sig, err := base64.StdEncoding.DecodeString(rel.Signature)
	if err != nil || !ed25519.Verify(key, signedMessage(platform(), rel.Version, rel.SHA256), sig) {
		return "", fmt.Errorf("release %s is not signed with the HR Lens release key; ignoring it", rel.Version)
	}

	req, _ = http.NewRequestWithContext(ctx, http.MethodGet, a.cfg.Server+"/api/agent/update/download"+query, nil)
	if resp, err = a.do(req); err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download: server answered %s", resp.Status)
	}
	if err := replaceVerified(bin, io.LimitReader(resp.Body, maxUpdateBytes), rel.SHA256); err != nil {
		return "", err
	}
	return rel.Version, nil
}

// replaceVerified writes body next to bin and, only if its SHA-256 is sha, renames it over bin.
// replaceFile takes care of a running bin.
func replaceVerified(bin string, body io.Reader, sha string) error {
	tmp := bin + ".download"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	defer os.Remove(tmp) // no-op after the rename
	h := sha256.New()
	_, err = io.Copy(io.MultiWriter(f, h), body)
	if closeErr := f.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != strings.ToLower(sha) {
		return errors.New("download does not match the signed checksum; ignoring it")
	}
	return replaceFile(tmp, bin)
}

var backupSeq uint64

// replaceWithBackup renames src over dst, moving an existing dst to a unique .old.* backup
// first so a running Windows binary can be replaced repeatedly. Prior unlocked backups are
// cleaned up best-effort, and if moving src into place fails, dst is rolled back from the backup.
func replaceWithBackup(src, dst string, rename func(string, string) error, remove func(string) error) error {
	if rename == nil {
		rename = os.Rename
	}
	if remove == nil {
		remove = os.Remove
	}
	cleanupOldBackups(dst, remove)

	var backup string
	if _, err := os.Stat(dst); err == nil {
		backup, err = uniqueBackupPath(dst)
		if err != nil {
			return err
		}
		if err := rename(dst, backup); err != nil {
			return err
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}

	if err := rename(src, dst); err != nil {
		if backup != "" {
			if rbErr := rename(backup, dst); rbErr != nil {
				return fmt.Errorf("%w (rollback failed: %v)", err, rbErr)
			}
		}
		return err
	}
	return nil
}

func cleanupOldBackups(dst string, remove func(string) error) {
	dir := filepath.Dir(dst)
	base := filepath.Base(dst)
	_ = remove(dst + ".old") // left by an earlier replace; fails harmlessly while it still runs
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	prefix := base + ".old."
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), prefix) {
			_ = remove(filepath.Join(dir, e.Name()))
		}
	}
}

func uniqueBackupPath(dst string) (string, error) {
	ts := time.Now().UTC().UnixNano()
	for i := 0; i < 1000; i++ {
		seq := atomic.AddUint64(&backupSeq, 1)
		candidate := fmt.Sprintf("%s.old.%d-%d", dst, ts, seq)
		_, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", err
		}
	}
	return "", fmt.Errorf("cannot allocate backup path for %s", dst)
}

// newer reports whether version a (e.g. "0.10.2") is later than b; "dev" and malformed versions never are.
func newer(a, b string) bool {
	pa, okA := parseVersion(a)
	pb, okB := parseVersion(b)
	if !okA || !okB {
		return false
	}
	for i := range pa {
		if pa[i] != pb[i] {
			return pa[i] > pb[i]
		}
	}
	return false
}

func parseVersion(v string) ([3]int, bool) {
	var out [3]int
	parts := strings.Split(v, ".")
	if len(parts) != 3 {
		return out, false
	}
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return out, false
		}
		out[i] = n
	}
	return out, true
}

func samePath(a, b string) bool {
	ra, errA := filepath.EvalSymlinks(a)
	rb, errB := filepath.EvalSymlinks(b)
	return errA == nil && errB == nil && ra == rb
}
