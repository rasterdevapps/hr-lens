package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestSupervisorChild(t *testing.T) {
	switch os.Getenv("HRLENS_SUPERVISOR_TEST") {
	case "eof":
		if _, err := io.Copy(io.Discard, os.Stdin); err != nil {
			os.Exit(3)
		}
		os.Exit(0)
	case "restart":
		os.Exit(exitRestart)
	case "fatal":
		os.Exit(exitFatal)
	case "success":
		os.Exit(0)
	}
}

func TestPortableSupervisorShutdown(t *testing.T) {
	signals := make(chan os.Signal, 1)
	started := make(chan struct{})
	go func() { <-started; signals <- os.Interrupt }()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	before := time.Now()
	err := superviseCmd(log, func() *exec.Cmd {
		cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorChild$")
		cmd.Env = append(os.Environ(), "HRLENS_SUPERVISOR_TEST=eof")
		close(started)
		return cmd
	}, signals, time.Millisecond, time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if time.Since(before) > 5*time.Second {
		t.Fatal("cooperative shutdown exceeded five seconds")
	}
}

func TestPortableSupervisorRestart(t *testing.T) {
	count := 0
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	err := superviseCmd(log, func() *exec.Cmd {
		mode := "restart"
		if count == 2 {
			mode = "success"
		}
		count++
		cmd := exec.Command(os.Args[0], "-test.run=^TestSupervisorChild$")
		cmd.Env = append(os.Environ(), "HRLENS_SUPERVISOR_TEST="+mode)
		return cmd
	}, make(chan os.Signal), time.Millisecond, time.Millisecond)
	if err != nil || count != 3 {
		t.Fatalf("starts=%d error=%v", count, err)
	}
}

func TestLifecycleCleanupPreservesUpdateRestart(t *testing.T) {
	ctx, restart := context.WithCancel(context.Background())
	userStopped := false
	closeWindow := stopWhenContextActive(ctx, func() { userStopped = true })
	restart()
	closeWindow()
	if userStopped {
		t.Fatal("update cleanup became a user quit")
	}
	closeActive := stopWhenContextActive(context.Background(), func() { userStopped = true })
	closeActive()
	if !userStopped {
		t.Fatal("unexpected window exit did not stop collection")
	}
}
