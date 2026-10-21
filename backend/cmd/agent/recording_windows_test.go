//go:build windows

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func syntheticDesktopFrame(frameIdx int, minX, minY, width, height int) *image.RGBA {
	rect := image.Rect(minX, minY, minX+width, minY+height)
	img := image.NewRGBA(rect)
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			r := uint8((x + frameIdx*29) % 256)
			g := uint8((y + frameIdx*53) % 256)
			b := uint8(((x ^ y) + frameIdx*17) % 256)
			i := y*img.Stride + x*4
			img.Pix[i] = r
			img.Pix[i+1] = g
			img.Pix[i+2] = b
			img.Pix[i+3] = 255
		}
	}
	return img
}

func resolveTestEncoderTools(t *testing.T) (ffmpegBin, ffprobeBin string) {
	t.Helper()
	requireEncoder := strings.TrimSpace(os.Getenv("HRLENS_REQUIRE_ENCODER_TESTS")) == "1"

	ffmpegBin = strings.TrimSpace(os.Getenv("HRLENS_TEST_FFMPEG"))
	if ffmpegBin == "" {
		if resolved, err := resolveFFmpegPath(""); err == nil {
			ffmpegBin = resolved
		}
	}
	ffprobeBin = strings.TrimSpace(os.Getenv("HRLENS_TEST_FFPROBE"))
	if ffprobeBin == "" && ffmpegBin != "" {
		candidate := filepath.Join(filepath.Dir(ffmpegBin), "ffprobe.exe")
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
			ffprobeBin = candidate
		}
	}
	if ffprobeBin == "" {
		if found, err := exec.LookPath("ffprobe.exe"); err == nil && filepath.IsAbs(found) {
			ffprobeBin = found
		}
	}

	if ffmpegBin == "" || ffprobeBin == "" {
		if requireEncoder {
			t.Fatalf("HRLENS_REQUIRE_ENCODER_TESTS=1 but encoder tools are missing (HRLENS_TEST_FFMPEG=%q, HRLENS_TEST_FFPROBE=%q)", ffmpegBin, ffprobeBin)
		}
		t.Skip("skipping Windows encoder test: HRLENS_TEST_FFMPEG / HRLENS_TEST_FFPROBE not configured")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	verifiedFFmpeg, err := probeFFmpegLibvpx(ctx, ffmpegBin)
	if err != nil {
		if requireEncoder {
			t.Fatalf("required ffmpeg libvpx probe failed for %q: %v", ffmpegBin, err)
		}
		t.Skipf("skipping Windows encoder test: %v", err)
	}
	if info, err := os.Stat(ffprobeBin); err != nil || info.IsDir() {
		if requireEncoder {
			t.Fatalf("required ffprobe binary is not usable at %q: %v", ffprobeBin, err)
		}
		t.Skipf("skipping Windows encoder test: ffprobe unavailable at %q: %v", ffprobeBin, err)
	}
	return verifiedFFmpeg, filepath.Clean(ffprobeBin)
}

type ffprobeOutput struct {
	Streams []struct {
		CodecName     string `json:"codec_name"`
		CodecType     string `json:"codec_type"`
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		PixFmt        string `json:"pix_fmt"`
		NbReadFrames  string `json:"nb_read_frames"`
		NbReadPackets string `json:"nb_read_packets"`
	} `json:"streams"`
	Format struct {
		FormatName string `json:"format_name"`
	} `json:"format"`
}

func verifyWebMRecording(t *testing.T, ffmpegBin, ffprobeBin, webmPath string) (frames int, width, height int) {
	t.Helper()
	info, err := os.Stat(webmPath)
	if err != nil {
		t.Fatalf("stat recorded WebM %s: %v", webmPath, err)
	}
	if info.Size() <= 0 {
		t.Fatalf("recorded WebM %s is empty", webmPath)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	probeCmd := exec.CommandContext(
		ctx,
		ffprobeBin,
		"-v", "error",
		"-select_streams", "v:0",
		"-count_frames",
		"-count_packets",
		"-show_entries", "format=format_name:stream=codec_name,codec_type,width,height,pix_fmt,nb_read_frames,nb_read_packets",
		"-of", "json",
		webmPath,
	)
	out, err := probeCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe failed on %s: %v\n%s", webmPath, err, string(out))
	}

	var parsed ffprobeOutput
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatalf("unmarshal ffprobe JSON: %v\n%s", err, string(out))
	}
	if !strings.Contains(strings.ToLower(parsed.Format.FormatName), "webm") && !strings.Contains(strings.ToLower(parsed.Format.FormatName), "matroska") {
		t.Fatalf("format_name = %q, want webm/matroska", parsed.Format.FormatName)
	}
	if len(parsed.Streams) != 1 {
		t.Fatalf("expected 1 video stream, got %d (%s)", len(parsed.Streams), string(out))
	}
	st := parsed.Streams[0]
	if st.CodecName != "vp8" {
		t.Fatalf("codec_name = %q, want vp8", st.CodecName)
	}
	if st.Width <= 0 || st.Width > maxRecordingWidth || st.Width%2 != 0 {
		t.Fatalf("unexpected stream width %d (want even and <= %d)", st.Width, maxRecordingWidth)
	}
	if st.Height <= 0 || st.Height%2 != 0 {
		t.Fatalf("unexpected stream height %d (want positive even)", st.Height)
	}

	frameCount, _ := strconv.Atoi(st.NbReadFrames)
	if frameCount <= 0 {
		frameCount, _ = strconv.Atoi(st.NbReadPackets)
	}
	if frameCount <= 0 {
		t.Fatalf("expected nonzero decoded frames in %s, got nb_read_frames=%q nb_read_packets=%q", webmPath, st.NbReadFrames, st.NbReadPackets)
	}

	decodeCmd := exec.CommandContext(ctx, ffmpegBin, "-v", "error", "-i", webmPath, "-f", "null", "-")
	if decOut, err := decodeCmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg decode verification failed for %s: %v\n%s", webmPath, err, string(decOut))
	}
	return frameCount, st.Width, st.Height
}

func TestWindowsRecording(t *testing.T) {
	ffmpegBin, ffprobeBin := resolveTestEncoderTools(t)

	t.Run("normal", func(t *testing.T) {
		dir := t.TempDir()
		partPath := filepath.Join(dir, "recording-20261021T120000.webm.part")

		var seq atomic.Int32
		provider := func(ctx context.Context, suspended func() bool) (*image.RGBA, error) {
			if suspended != nil && suspended() {
				return nil, errDesktopSuspended
			}
			n := int(seq.Add(1))
			// Negative-origin multi-monitor virtual desktop (2560x721 -> scaled <=1280 and even height).
			return syntheticDesktopFrame(n, -1280, -120, 2560, 721), nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		err := recordWithProvider(
			ctx,
			ffmpegBin,
			partPath,
			350*time.Millisecond,
			100*time.Millisecond,
			func() bool { return false },
			provider,
		)
		if err != nil {
			t.Fatalf("recordWithProvider (normal) failed: %v", err)
		}

		frames, w, h := verifyWebMRecording(t, ffmpegBin, ffprobeBin, partPath)
		if frames < 2 {
			t.Fatalf("expected at least 2 frames in normal recording, got %d", frames)
		}
		if w != 1280 || h%2 != 0 {
			t.Fatalf("recorded dimensions = %dx%d, want 1280x(even)", w, h)
		}
	})

	t.Run("early_stop", func(t *testing.T) {
		dir := t.TempDir()
		partPath := filepath.Join(dir, "recording-20261021T120500.webm.part")

		var seq atomic.Int32
		var locked atomic.Bool
		provider := func(ctx context.Context, suspended func() bool) (*image.RGBA, error) {
			if suspended != nil && suspended() {
				return nil, errDesktopSuspended
			}
			n := int(seq.Add(1))
			if n >= 3 {
				// Simulate lock / secure desktop transition after 2 frames have been captured.
				locked.Store(true)
			}
			return syntheticDesktopFrame(n, -640, 0, 1920, 1080), nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		start := time.Now()
		err := recordWithProvider(
			ctx,
			ffmpegBin,
			partPath,
			20*time.Second,
			80*time.Millisecond,
			locked.Load,
			provider,
		)
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("recordWithProvider (early_stop) failed: %v", err)
		}
		if elapsed > 10*time.Second {
			t.Fatalf("early_stop took too long (%v); expected prompt finalization", elapsed)
		}

		frames, w, h := verifyWebMRecording(t, ffmpegBin, ffprobeBin, partPath)
		if frames < 1 {
			t.Fatalf("expected nonzero frames in early-stopped recording, got %d", frames)
		}
		if w != 1280 || h != 720 {
			t.Fatalf("recorded dimensions = %dx%d, want 1280x720", w, h)
		}
	})
}

func TestWindowsRecordingCancelAndDisplayChange(t *testing.T) {
	ffmpeg, ffprobe := resolveTestEncoderTools(t)
	for _, mode := range []string{"context_cancel", "display_change"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			frames := 0
			provider := func(context.Context, func() bool) (*image.RGBA, error) {
				frames++
				if mode == "context_cancel" && frames == 3 {
					cancel()
				}
				w, h := 1280, 720
				if mode == "display_change" && frames > 1 {
					w, h = 640, 960
				}
				return syntheticDesktopFrame(frames, -100, -50, w, h), nil
			}
			path := filepath.Join(t.TempDir(), "synthetic.webm.part")
			err := recordWithProvider(ctx, ffmpeg, path, 800*time.Millisecond, 150*time.Millisecond, func() bool { return false }, provider)
			if err != nil {
				t.Fatal(err)
			}
			n, w, h := verifyWebMRecording(t, ffmpeg, ffprobe, path)
			if n < 2 || w != 1280 || h != 720 {
				t.Fatalf("unexpected recording: %d frames, %dx%d", n, w, h)
			}
		})
	}
}

func TestWindowsCaptureGuardAndScaling(t *testing.T) {
	dir := t.TempDir()

	// 1. Valid synthetic multi-monitor capture with negative origin and maxWidth scaling.
	shotPath := filepath.Join(dir, "shot.jpg")
	provider := func(ctx context.Context, suspended func() bool) (*image.RGBA, error) {
		img := image.NewRGBA(image.Rect(-1920, -200, 1920, 1000)) // 3840x1200
		img.SetRGBA(-1920, -200, color.RGBA{R: 250, G: 20, B: 30, A: 255})
		return img, nil
	}
	if err := screenshotWithProvider(context.Background(), shotPath, 960, 75, func() bool { return false }, provider); err != nil {
		t.Fatalf("screenshotWithProvider failed: %v", err)
	}
	raw, err := os.ReadFile(shotPath)
	if err != nil {
		t.Fatalf("read screenshot: %v", err)
	}
	decoded, err := jpeg.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode JPEG: %v", err)
	}
	if decoded.Bounds().Dx() != 960 || decoded.Bounds().Dy() != 300 {
		t.Fatalf("scaled JPEG bounds = %v, want 960x300", decoded.Bounds())
	}

	// 2. Rejects capture when desktop transitions to secure/locked mid-acquisition and leaves no file.
	unsafePath := filepath.Join(dir, "unsafe.jpg")
	midLock := false
	transitionProvider := func(ctx context.Context, suspended func() bool) (*image.RGBA, error) {
		midLock = true // secure desktop activates during capture
		return syntheticDesktopFrame(1, 0, 0, 800, 600), nil
	}
	err = screenshotWithProvider(context.Background(), unsafePath, 0, 60, func() bool { return midLock }, transitionProvider)
	if !errors.Is(err, errDesktopSuspended) {
		t.Fatalf("expected errDesktopSuspended on mid-capture lock, got %v", err)
	}
	if _, statErr := os.Stat(unsafePath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("unsafe screenshot must not be written to disk, statErr=%v", statErr)
	}

	// 3. Rejects cancelled context and cleans up.
	cancelPath := filepath.Join(dir, "cancelled.jpg")
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := screenshotWithProvider(cctx, cancelPath, 0, 60, func() bool { return false }, provider); err == nil {
		t.Fatal("expected error for cancelled context")
	}
	if _, statErr := os.Stat(cancelPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("cancelled screenshot must not exist, statErr=%v", statErr)
	}
}

func TestWindowsFFmpegResolution(t *testing.T) {
	if _, err := resolveFFmpegPath(`relative\path\ffmpeg.exe`); err == nil {
		t.Fatal("expected relative ffmpeg path to be rejected")
	}
	if _, err := resolveFFmpegPath(t.TempDir()); err == nil {
		t.Fatal("expected directory path to be rejected")
	}

	cwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	fakeCwdFFmpeg := filepath.Join(cwd, "ffmpeg.exe")
	if _, err := os.Stat(fakeCwdFFmpeg); errors.Is(err, os.ErrNotExist) {
		// Create a temporary placeholder in a temp dir and chdir to verify CWD rejection.
		tmp := t.TempDir()
		placeholder := filepath.Join(tmp, "ffmpeg.exe")
		if err := os.WriteFile(placeholder, []byte("not-an-exe"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chdir(tmp); err == nil {
			defer os.Chdir(cwd)
			t.Setenv("PATH", tmp)
			t.Setenv(ffmpegEnv, "")
			if got, err := resolveFFmpegPath(""); err == nil {
				t.Fatalf("expected CWD ffmpeg.exe discovery to be rejected, got %q", got)
			}
		}
	}
}
