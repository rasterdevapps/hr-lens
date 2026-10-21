package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Every running session owns its queue via a cross-process file lock. A later
// session may drain abandoned queues after logoff/reboot without importing their
// attendance state into its own session. The OS releases ownership after a crash.
func ownSpool(dir string) (func(), error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	return acquireUpdateLock(filepath.Join(dir, ".owner"))
}

func (a *agent) recoverSpools(ctx context.Context) {
	// Linux has one queue per user and already loads it directly.
	if !strings.HasPrefix(filepath.Base(a.spool), "session-") {
		return
	}
	every(ctx, time.Minute, func() {
		if err := a.drainAbandonedSpools(ctx); err != nil && ctx.Err() == nil {
			a.log.Warn("recovering an inactive session queue failed; will retry", "error", err)
		}
	})
}

func (a *agent) drainAbandonedSpools(ctx context.Context) error {
	root := filepath.Dir(a.spool)
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "session-") {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		if dir == a.spool {
			continue
		}
		release, err := ownSpool(dir)
		if err != nil {
			continue
		} // another session or recovery worker owns it
		err = a.drainSpool(ctx, dir)
		release()
		if err != nil {
			return fmt.Errorf("%s: %w", entry.Name(), err)
		}
	}
	return nil
}

func (a *agent) drainSpool(ctx context.Context, dir string) error {
	q := &agent{cfg: a.cfg, http: a.http, log: a.log, spool: dir,
		input: map[time.Time]*inputMinute{}, maxSpoolBytes: a.maxSpoolBytes}
	if err := q.loadSnapshot(); err != nil {
		return err
	}
	// Queue ownership proves these partial files cannot belong to a live recorder.
	parts, err := filepath.Glob(filepath.Join(dir, "*.part"))
	if err != nil {
		return err
	}
	for _, p := range parts {
		if err := removeIfExists(p); err != nil {
			return err
		}
	}
	q.enforceSpoolLimit()
	for ctx.Err() == nil {
		before := len(q.events) + len(q.presence) + len(q.input)
		if before == 0 {
			break
		}
		q.flushInput(ctx, true)
		q.flushActivity(ctx)
		after := len(q.events) + len(q.presence) + len(q.input)
		if after >= before {
			return fmt.Errorf("pending batches have not been acknowledged")
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	files, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, f := range files {
		if f.IsDir() {
			continue
		}
		path := filepath.Join(dir, f.Name())
		ext := filepath.Ext(path)
		if ext == ".jpg" || ext == ".webm" {
			if err := q.upload(ctx, path); err != nil {
				return err
			}
		} else if ext == ".uploaded" {
			if err := removeUpload(strings.TrimSuffix(path, ".uploaded")); err != nil {
				return err
			}
		}
	}
	return nil
}

// Publish complete metadata and media together from the uploader's perspective.
// A recorder never exposes a partial file with an uploadable extension.
func (a *agent) publishCapture(part, path string, meta map[string]string) error {
	a.spoolMu.Lock()
	defer a.spoolMu.Unlock()
	if err := writeMeta(path, meta); err != nil {
		os.Remove(part)
		return err
	}
	if err := replaceStateFile(part, path); err != nil {
		os.Remove(part)
		os.Remove(path + ".json")
		return err
	}
	return nil
}
