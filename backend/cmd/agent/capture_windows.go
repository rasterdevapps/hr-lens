//go:build windows

package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const (
	ffmpegEnv             = "HRLENS_FFMPEG"
	maxRecordingWidth     = 1280
	defaultJPEGQuality    = 60
	recordingJPEGQuality  = 60
	ffmpegProbeTimeout    = 5 * time.Second
	ffmpegFrameWriteLimit = 3 * time.Second
	ffmpegFinalizeTimeout = 3 * time.Second
	maxFFmpegStderrBytes  = 16 << 10
)

var (
	procSetProcessDpiAwarenessContext = user32.NewProc("SetProcessDpiAwarenessContext")
	processDPIOnce                    sync.Once
)

// frameProvider acquires a full-desktop RGBA frame while honoring the suspended guard.
type frameProvider func(ctx context.Context, suspended func() bool) (*image.RGBA, error)

func ensureProcessDPI() {
	processDPIOnce.Do(func() {
		if err := procSetProcessDpiAwarenessContext.Find(); err == nil {
			// DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2 = -4 (^uintptr(3))
			_, _, _ = procSetProcessDpiAwarenessContext.Call(^uintptr(3))
		}
	})
}

func clampJPEGQuality(q int) int {
	if q <= 0 {
		return defaultJPEGQuality
	}
	if q < 1 {
		return 1
	}
	if q > 100 {
		return 100
	}
	return q
}

// ensureEvenRGBA returns an RGBA image with even width and height (>= 2) as required by yuv420p.
func ensureEvenRGBA(src *image.RGBA) (*image.RGBA, error) {
	if src == nil {
		return nil, errors.New("nil frame")
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 {
		return nil, errors.New("empty frame dimensions")
	}
	evenW := w &^ 1
	evenH := h &^ 1
	if evenW == w && evenH == h && b.Min.X == 0 && b.Min.Y == 0 {
		return src, nil
	}
	if evenW < 2 {
		evenW = 2
	}
	if evenH < 2 {
		evenH = 2
	}
	dst := image.NewRGBA(image.Rect(0, 0, evenW, evenH))
	for y := 0; y < evenH; y++ {
		srcY := b.Min.Y + min(y, h-1)
		for x := 0; x < evenW; x++ {
			srcX := b.Min.X + min(x, w-1)
			dst.SetRGBA(x, y, src.RGBAAt(srcX, srcY))
		}
	}
	return dst, nil
}

// captureJPEG captures a frame via provider, verifies suspension and context cancellation before
// and after acquisition, scales to maxWidth, and encodes a JPEG in memory.
func captureJPEG(ctx context.Context, maxWidth, quality int, evenDims bool, suspended func() bool, provider frameProvider) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if suspended == nil || suspended() {
		return nil, errDesktopSuspended
	}
	if provider == nil {
		ensureProcessDPI()
		provider = captureDesktop
	}
	img, err := provider(ctx, suspended)
	if err != nil {
		return nil, err
	}
	if img == nil {
		return nil, errors.New("captured frame is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if suspended() {
		return nil, errDesktopSuspended
	}
	b := img.Bounds()
	if _, err := desktopBounds(b.Min.X, b.Min.Y, b.Dx(), b.Dy()); err != nil {
		return nil, err
	}
	scaled := resizeDesktop(img, maxWidth)
	if evenDims {
		scaled, err = ensureEvenRGBA(scaled)
		if err != nil {
			return nil, err
		}
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if suspended() {
		return nil, errDesktopSuspended
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, scaled, &jpeg.Options{Quality: clampJPEGQuality(quality)}); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if suspended() {
		return nil, errDesktopSuspended
	}
	return buf.Bytes(), nil
}

func screenshotWithProvider(ctx context.Context, path string, maxWidth, quality int, suspended func() bool, provider frameProvider) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("empty screenshot path")
	}
	data, err := captureJPEG(ctx, maxWidth, quality, false, suspended, provider)
	if err != nil {
		_ = os.Remove(path)
		return err
	}
	if err := atomicWriteFile(path, data, 0o600); err != nil {
		_ = os.Remove(path)
		return err
	}
	if err := ctx.Err(); err != nil {
		_ = os.Remove(path)
		return err
	}
	if suspended != nil && suspended() {
		_ = os.Remove(path)
		return errDesktopSuspended
	}
	return nil
}

// screenshot captures the virtual desktop across all monitors and writes a JPEG to path.
func (s *screen) screenshot(ctx context.Context, path string, maxWidth, quality int) error {
	if s == nil {
		return errDesktopSuspended
	}
	return screenshotWithProvider(ctx, path, maxWidth, quality, s.captureSuspended, captureDesktop)
}

// resolveFFmpegPath validates an explicit absolute FFmpeg binary path (or HRLENS_FFMPEG),
// or discovers a trusted installed ffmpeg.exe on PATH while rejecting relative and CWD paths.
func resolveFFmpegPath(configured string) (string, error) {
	explicit := strings.TrimSpace(configured)
	if explicit == "" {
		explicit = strings.TrimSpace(os.Getenv(ffmpegEnv))
	}
	if explicit != "" {
		if !filepath.IsAbs(explicit) {
			return "", fmt.Errorf("%s must be an absolute path: %q", ffmpegEnv, explicit)
		}
		clean := filepath.Clean(explicit)
		info, err := os.Stat(clean)
		if err != nil {
			return "", fmt.Errorf("ffmpeg binary not accessible at %q: %w", clean, err)
		}
		if info.IsDir() {
			return "", fmt.Errorf("ffmpeg path %q is a directory", clean)
		}
		return clean, nil
	}

	found, err := exec.LookPath("ffmpeg.exe")
	if err != nil {
		return "", fmt.Errorf("ffmpeg.exe not found in PATH: %w", err)
	}
	if !filepath.IsAbs(found) {
		return "", fmt.Errorf("refusing relative ffmpeg path %q", found)
	}
	clean := filepath.Clean(found)
	if cwd, cwdErr := os.Getwd(); cwdErr == nil && cwd != "" {
		if strings.EqualFold(filepath.Clean(filepath.Dir(clean)), filepath.Clean(cwd)) {
			return "", fmt.Errorf("refusing ffmpeg binary from current working directory: %q", clean)
		}
	}
	info, err := os.Stat(clean)
	if err != nil {
		return "", err
	}
	if info.IsDir() {
		return "", fmt.Errorf("ffmpeg path %q is a directory", clean)
	}
	return clean, nil
}

// probeFFmpegLibvpx verifies that the resolved FFmpeg executable runs and provides the libvpx encoder.
func probeFFmpegLibvpx(ctx context.Context, configured string) (string, error) {
	bin, err := resolveFFmpegPath(configured)
	if err != nil {
		return "", err
	}
	probeCtx, cancel := context.WithTimeout(ctx, ffmpegProbeTimeout)
	defer cancel()

	cmd := exec.CommandContext(probeCtx, bin, "-hide_banner", "-encoders")
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = 2 * time.Second
	var out limitedBuffer
	out.max = 256 << 10
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("probing ffmpeg encoders failed: %w", err)
	}
	if !hasLibvpxEncoder(out.String()) {
		return "", errors.New("ffmpeg does not support the libvpx VP8 encoder")
	}
	return bin, nil
}

func hasLibvpxEncoder(encodersOutput string) bool {
	for _, line := range strings.Split(encodersOutput, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && strings.HasPrefix(fields[0], "V") && fields[1] == "libvpx" {
			return true
		}
	}
	return false
}

// probeRecordingEncoder checks whether a usable FFmpeg with libvpx is available at runtime.
func probeRecordingEncoder(configured string) (string, bool) {
	bin, err := probeFFmpegLibvpx(context.Background(), configured)
	if err != nil {
		return "", false
	}
	return bin, true
}

func (s *screen) canRecord() bool {
	configured := ""
	if s != nil {
		configured = s.ffmpegPath
	}
	bin, ok := probeRecordingEncoder(configured)
	if ok && s != nil && s.ffmpegPath == "" {
		s.ffmpegPath = bin
	}
	return ok
}

type limitedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
	max int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	total := len(p)
	limit := b.max
	if limit <= 0 {
		limit = maxFFmpegStderrBytes
	}
	rem := limit - b.buf.Len()
	if rem > 0 {
		if len(p) > rem {
			p = p[:rem]
		}
		_, _ = b.buf.Write(p)
	}
	return total, nil
}

func (b *limitedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// record captures the desktop at 1 fps into a VP8 WebM file at path for up to length,
// stopping early and finalizing a playable WebM if ctx ends or stop() / captureSuspended() turns true.
func (s *screen) record(ctx context.Context, path string, length time.Duration, stop func() bool) error {
	if s == nil {
		return errDesktopSuspended
	}
	suspended := func() bool {
		if s.captureSuspended() {
			return true
		}
		return stop != nil && stop()
	}
	return recordWithProvider(ctx, s.ffmpegPath, path, length, time.Second, suspended, captureDesktop)
}

func recordWithProvider(ctx context.Context, configuredFFmpeg, path string, length, frameInterval time.Duration, suspended func() bool, provider frameProvider) error {
	if strings.TrimSpace(path) == "" {
		return errors.New("empty recording path")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if suspended == nil || suspended() {
		return errDesktopSuspended
	}
	if length <= 0 {
		return errors.New("recording length must be positive")
	}
	if frameInterval <= 0 {
		frameInterval = time.Second
	}

	bin, err := probeFFmpegLibvpx(ctx, configuredFFmpeg)
	if err != nil {
		return err
	}
	firstFrame, err := captureJPEG(ctx, maxRecordingWidth, recordingJPEGQuality, true, suspended, provider)
	if err != nil {
		return err
	}
	geometry, err := jpeg.DecodeConfig(bytes.NewReader(firstFrame))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	_ = os.Remove(path)

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-f", "image2pipe",
		"-framerate", "1",
		"-c:v", "mjpeg",
		"-i", "pipe:0",
		"-an",
		"-vf", fmt.Sprintf("scale=%d:%d:force_original_aspect_ratio=decrease:force_divisible_by=2,pad=%d:%d:(ow-iw)/2:(oh-ih)/2", geometry.Width, geometry.Height, geometry.Width, geometry.Height),
		"-c:v", "libvpx",
		"-pix_fmt", "yuv420p",
		"-b:v", "1M",
		"-crf", "24",
		"-g", "30",
		"-deadline", "realtime",
		"-cpu-used", "8",
		"-f", "webm",
		"-y",
		path,
	}

	// Use exec.Command (not CommandContext) so context cancellation allows closing stdin
	// and finalizing the WebM container cleanly before falling back to process termination.
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	cmd.WaitDelay = 2 * time.Second
	cmd.Stdout = io.Discard
	var stderr limitedBuffer
	stderr.max = maxFFmpegStderrBytes
	cmd.Stderr = &stderr

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		return err
	}

	done := make(chan struct{})
	var processErr error
	go func() { processErr = cmd.Wait(); close(done) }()

	var closeStdinOnce sync.Once
	closeStdin := func() {
		closeStdinOnce.Do(func() {
			_ = stdin.Close()
		})
	}

	writeFrame := func(frame []byte) error {
		writeErrCh := make(chan error, 1)
		go func() {
			_, werr := stdin.Write(frame)
			writeErrCh <- werr
		}()
		timer := time.NewTimer(ffmpegFrameWriteLimit)
		defer timer.Stop()
		select {
		case werr := <-writeErrCh:
			return werr
		case <-done:
			procErr := processErr
			closeStdin()
			if procErr != nil {
				return procErr
			}
			return io.ErrClosedPipe
		case <-ctx.Done():
			closeStdin()
			return ctx.Err()
		case <-timer.C:
			closeStdin()
			_ = cmd.Process.Kill()
			return errors.New("timed out writing frame to ffmpeg stdin")
		}
	}

	targetFrames := max(1, int((length+frameInterval-1)/frameInterval))
	deadline := time.Now().Add(length)
	ticker := time.NewTicker(frameInterval)
	defer ticker.Stop()

	pollInterval := 200 * time.Millisecond
	if frameInterval < pollInterval {
		pollInterval = frameInterval / 2
		if pollInterval < 10*time.Millisecond {
			pollInterval = 10 * time.Millisecond
		}
	}
	poll := time.NewTicker(pollInterval)
	defer poll.Stop()

	framesWritten := 0
	var captureLoopErr error

captureLoop:
	for framesWritten < targetFrames {
		if ctx.Err() != nil || suspended() {
			break
		}
		frame := firstFrame
		var capErr error
		if framesWritten > 0 {
			frame, capErr = captureJPEG(ctx, maxRecordingWidth, recordingJPEGQuality, true, suspended, provider)
		}
		if capErr != nil {
			if errors.Is(capErr, errDesktopSuspended) || errors.Is(capErr, context.Canceled) || errors.Is(capErr, context.DeadlineExceeded) {
				break
			}
			captureLoopErr = capErr
			break
		}
		if werr := writeFrame(frame); werr != nil {
			if errors.Is(werr, context.Canceled) || errors.Is(werr, context.DeadlineExceeded) {
				break
			}
			captureLoopErr = werr
			break
		}
		framesWritten++
		if framesWritten >= targetFrames || !time.Now().Before(deadline) {
			break
		}

		for {
			select {
			case <-ctx.Done():
				break captureLoop
			case <-done:
				procErr := processErr
				closeStdin()
				_ = os.Remove(path)
				if procErr != nil {
					return fmt.Errorf("ffmpeg exited early: %w: %s", procErr, strings.TrimSpace(stderr.String()))
				}
				return errors.New("ffmpeg exited unexpectedly during recording")
			case <-poll.C:
				if suspended() || !time.Now().Before(deadline) {
					break captureLoop
				}
			case <-ticker.C:
				if suspended() || !time.Now().Before(deadline) {
					break captureLoop
				}
				continue captureLoop
			}
		}
	}

	closeStdin()

	var waitErr error
	select {
	case <-done:
		waitErr = processErr
	case <-time.After(ffmpegFinalizeTimeout):
		_ = cmd.Process.Kill()
		<-done
		_ = os.Remove(path)
		return errors.New("recorder did not stop in time")
	}

	if framesWritten == 0 {
		_ = os.Remove(path)
		if captureLoopErr != nil {
			return captureLoopErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		return errDesktopSuspended
	}

	if captureLoopErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("recording failed: %w (%s)", captureLoopErr, strings.TrimSpace(stderr.String()))
	}

	if waitErr != nil {
		_ = os.Remove(path)
		return fmt.Errorf("ffmpeg failed: %w: %s", waitErr, strings.TrimSpace(stderr.String()))
	}

	info, statErr := os.Stat(path)
	if statErr != nil || info.Size() == 0 {
		_ = os.Remove(path)
		if statErr != nil {
			return statErr
		}
		return errors.New("recorded file is empty")
	}
	return nil
}
