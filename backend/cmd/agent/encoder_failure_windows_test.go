package main

import (
	"context"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The test executable doubles as a deliberately broken encoder. It never starts
// the desktop agent, receives keyboard input or captures a screen.
func TestMain(m *testing.M) {
	if mode := os.Getenv("HRLENS_FAKE_ENCODER"); mode != "" {
		for _, arg := range os.Args[1:] {
			if arg == "-encoders" {
				fmt.Println(" V..... libvpx synthetic-test-encoder")
				os.Exit(0)
			}
		}
		if mode == "blocked_stdin" {
			time.Sleep(time.Minute)
		}
		os.Exit(23)
	}
	os.Exit(m.Run())
}

func TestWindowsEncoderFailureBounded(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"early_exit", "blocked_stdin"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("HRLENS_FAKE_ENCODER", mode)
			path := filepath.Join(t.TempDir(), "broken.webm.part")
			provider := func(context.Context, func() bool) (*image.RGBA, error) {
				img := image.NewRGBA(image.Rect(0, 0, 1280, 720))
				var state uint32 = 42
				for i := range img.Pix {
					state ^= state << 13
					state ^= state >> 17
					state ^= state << 5
					img.Pix[i] = byte(state)
				}
				for i := 3; i < len(img.Pix); i += 4 {
					img.Pix[i] = 255
				}
				return img, nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			start := time.Now()
			err := recordWithProvider(ctx, exe, path, 20*time.Second, time.Second, func() bool { return false }, provider)
			if err == nil {
				t.Fatal("broken encoder succeeded")
			}
			if time.Since(start) > 9*time.Second {
				t.Fatal("encoder failure did not return promptly", err)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("invalid output retained", err)
			}
		})
	}
}
