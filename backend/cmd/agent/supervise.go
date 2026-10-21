package main

import (
	"errors"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

// `hrlens-agent run` starts a second copy of itself (the worker, which does the capturing) and
// starts it again if it crashes, so a fault does not stop capture until the next sign-in. The
// worker's exit code says what happened:
const (
	workerEnv   = "HRLENS_AGENT_WORKER"
	exitRestart = 75 // a new version was installed: start the worker again from the same path
	exitFatal   = 78 // cannot run here (not registered, no display, already running): give up
)

var errRestart = errors.New("restart requested")

const (
	firstRetry = 5 * time.Second
	lastRetry  = 5 * time.Minute
	healthyRun = 10 * time.Minute // a worker that ran this long starts the waits from firstRetry again
)

func supervise(log *slog.Logger) error {
	// Resolved once: after an update the file at this path is the new version, while
	// os.Executable() would still point at the replaced one
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		return err
	}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(signals)
	return superviseCmd(log, func() *exec.Cmd {
		cmd := exec.Command(exe, "run")
		cmd.Env = append(os.Environ(), workerEnv+"=1")
		if _, err := os.Stderr.Stat(); err == nil { // the Windows background copy has no console
			cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		}
		return cmd
	}, signals, firstRetry, lastRetry)
}

// superviseCmd runs worker() until it stops on request (exit 0), cannot run (exitFatal) or a
// signal arrives, waiting longer after each crash in a row.
func superviseCmd(log *slog.Logger, worker func() *exec.Cmd, signals <-chan os.Signal, retry, maxRetry time.Duration) error {
	wait := retry
	for {
		cmd := worker()
		started := time.Now()
		if err := cmd.Start(); err != nil {
			return err
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()

		var err error
		select {
		case sig := <-signals:
			// Signing out: let the worker save its recording and send what is left
			cmd.Process.Signal(sig)
			<-done
			return nil
		case err = <-done:
		}

		code := 0
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			code = exitErr.ExitCode() // -1 when killed by a signal
		} else if err != nil {
			return err
		}
		switch code {
		case 0:
			return nil
		case exitFatal:
			return errors.New("the agent cannot run on this computer (see the message above)")
		case exitRestart:
			log.Info("starting the updated agent")
			wait = retry
			continue
		}
		if time.Since(started) >= healthyRun {
			wait = retry
		}
		log.Warn("agent stopped unexpectedly; starting it again", "exit", code, "in", wait)
		select {
		case <-signals:
			return nil
		case <-time.After(wait):
		}
		wait = min(wait*2, maxRetry)
	}
}
