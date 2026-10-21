//go:build windows

package main

import (
	"io"
	"os"
	"path/filepath"
)

const maxLogBytes = 5 << 20

// logOutput is where the agent's log goes. The background copy (hrlens-agentw.exe) has no
// console, so `run` also writes to agent.log in the agent's config folder (%APPDATA%\hrlens-agent).
func logOutput() io.Writer {
	if len(os.Args) < 2 || os.Args[1] != "run" {
		return os.Stderr
	}
	dir, err := configDir()
	if err != nil || os.MkdirAll(dir, 0o700) != nil {
		return os.Stderr
	}
	path := filepath.Join(dir, "agent.log")
	// The supervisor starts first: it starts a fresh file when the log has grown too big
	if fi, err := os.Stat(path); err == nil && fi.Size() > maxLogBytes && os.Getenv(workerEnv) == "" {
		os.Rename(path, path+".old")
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return os.Stderr
	}
	if _, err := os.Stderr.Stat(); err != nil {
		return f // no console
	}
	return io.MultiWriter(f, os.Stderr)
}
