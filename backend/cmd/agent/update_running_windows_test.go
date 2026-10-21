//go:build windows

package main

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestWindowsOldImageHelper(t *testing.T) {
	if os.Getenv("HRLENS_IMAGE_LOCK_TEST") != "1" {
		return
	}
	fmt.Fprintln(os.Stdout, "ready")
	_, _ = io.Copy(io.Discard, os.Stdin)
	os.Exit(0)
}

func TestWindowsTwoUpdatesWithRunningImages(t *testing.T) {
	self, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	dst := filepath.Join(dir, "agent.exe")
	if err := os.WriteFile(dst, self, 0o755); err != nil {
		t.Fatal(err)
	}
	startImage := func() func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, dst, "-test.run=^TestWindowsOldImageHelper$")
		cmd.Env = append(os.Environ(), "HRLENS_IMAGE_LOCK_TEST=1")
		input, err := cmd.StdinPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		output, err := cmd.StdoutPipe()
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		ready, err := bufio.NewReader(output).ReadString('\n')
		if err != nil || ready != "ready\n" {
			input.Close()
			cancel()
			cmd.Wait()
			t.Fatalf("image did not start: %q %v", ready, err)
		}
		return func() {
			input.Close()
			err := cmd.Wait()
			cancel()
			if err != nil {
				t.Error(err)
			}
		}
	}
	stopSupervisor := startImage()
	stoppedSupervisor := false
	defer func() {
		if !stoppedSupervisor {
			stopSupervisor()
		}
	}()
	for i := 0; i < 2; i++ {
		var stopWorker func()
		if i == 1 {
			stopWorker = startImage()
		}
		src := filepath.Join(dir, fmt.Sprintf("new-%d.exe", i))
		if err := os.WriteFile(src, self, 0o755); err != nil {
			t.Fatal(err)
		}
		err := replaceFile(src, dst)
		if stopWorker != nil {
			stopWorker()
		}
		if err != nil {
			t.Fatalf("replacement %d with running images: %v", i+1, err)
		}
	}
	stopSupervisor()
	stoppedSupervisor = true
	cleanupOldBackups(dst, os.Remove)
	backups, err := filepath.Glob(dst + ".old.*")
	if err != nil || len(backups) != 0 {
		t.Fatalf("retired backups not cleaned: %v %v", backups, err)
	}
	if _, err := os.Stat(dst); err != nil {
		t.Fatal("installed executable missing", err)
	}
}
