//go:build !windows

package main

import (
	"io"
	"log/slog"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// worker runs `sh -c script`, where $N is how many times it has been started before.
func superviseScript(t *testing.T, script string, signals chan os.Signal) (starts int, err error) {
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	err = superviseCmd(log, func() *exec.Cmd {
		cmd := exec.Command("sh", "-c", script)
		cmd.Env = append(os.Environ(), "N="+strings.Repeat("x", starts))
		starts++
		return cmd
	}, signals, time.Millisecond, 4*time.Millisecond)
	return starts, err
}

func TestSuperviseRestartsAfterCrash(t *testing.T) {
	// Crashes twice, then stops normally
	starts, err := superviseScript(t, `[ ${#N} -ge 2 ] && exit 0; exit 2`, make(chan os.Signal))
	if err != nil || starts != 3 {
		t.Fatalf("starts = %d, err = %v; want 3 starts and no error", starts, err)
	}
}

func TestSuperviseRestartsAfterUpdate(t *testing.T) {
	starts, err := superviseScript(t, `[ ${#N} -ge 1 ] && exit 0; exit 75`, make(chan os.Signal))
	if err != nil || starts != 2 {
		t.Fatalf("starts = %d, err = %v; want 2 starts and no error", starts, err)
	}
}

func TestSuperviseGivesUpWhenItCannotRun(t *testing.T) {
	starts, err := superviseScript(t, `exit 78`, make(chan os.Signal))
	if err == nil || starts != 1 {
		t.Fatalf("starts = %d, err = %v; want 1 start and an error", starts, err)
	}
}

func TestSuperviseStopsWorkerOnSignal(t *testing.T) {
	signals := make(chan os.Signal, 1)
	go func() { time.Sleep(200 * time.Millisecond); signals <- os.Interrupt }()
	start := time.Now()
	starts, err := superviseScript(t, `trap 'exit 0' INT; while :; do sleep 0.05; done`, signals)
	if err != nil || starts != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("starts = %d, err = %v after %s; want the worker stopped once", starts, err, time.Since(start))
	}
}
