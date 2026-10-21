//go:build linux

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Linux with X11: GStreamer (gst-launch-1.0) captures the screen and xprop reads the front window.
// Wayland sessions do not allow silent capture, so the agent refuses to start there.

// canCapture says whether this platform can take screenshots and recordings.
const canCapture = true

type window struct {
	app     string // process name, e.g. "code"
	appName string // friendly name, e.g. "Visual Studio Code"
	title   string
}

type screen struct {
	lockCmd []string // asks the screensaver whether the screen is locked; nil if unknown

	mu        sync.Mutex
	lockedAt  time.Time
	wasLocked bool
}

func newScreen() (*screen, error) {
	if os.Getenv("XDG_SESSION_TYPE") == "wayland" || (os.Getenv("WAYLAND_DISPLAY") != "" && os.Getenv("DISPLAY") == "") {
		return nil, errors.New("this is a Wayland session, which does not allow screen capture without asking each time; log in with an X11 (Xorg) session")
	}
	if os.Getenv("DISPLAY") == "" {
		return nil, errors.New("no display found (DISPLAY is not set): run the agent inside the desktop session")
	}
	for _, tool := range []string{"gst-launch-1.0", "xprop"} {
		if _, err := exec.LookPath(tool); err != nil {
			return nil, fmt.Errorf("%s is missing: install it (Ubuntu/Mint: sudo apt install gstreamer1.0-tools gstreamer1.0-plugins-good x11-utils)", tool)
		}
	}
	s := &screen{}
	for _, c := range [][]string{{"cinnamon-screensaver-command", "-q"}, {"gnome-screensaver-command", "-q"}, {"mate-screensaver-command", "-q"}, {"xfce4-screensaver-command", "-q"}} {
		if _, err := exec.LookPath(c[0]); err == nil {
			s.lockCmd = c
			break
		}
	}
	return s, nil
}

// locked reports whether the screensaver / lock screen is showing (checked at most every 3 seconds).
func (s *screen) locked() bool {
	if s.lockCmd == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if time.Since(s.lockedAt) < 3*time.Second {
		return s.wasLocked
	}
	out, _ := exec.Command(s.lockCmd[0], s.lockCmd[1:]...).Output()
	text := strings.ToLower(string(out))
	s.wasLocked = strings.Contains(text, "is active") || strings.Contains(text, "is locked")
	s.lockedAt = time.Now()
	return s.wasLocked
}

func (s *screen) attendanceLocked() bool { return s.locked() }
func (s *screen) captureSuspended() bool { return s.locked() }
func (s *screen) canCaptureNow() bool    { return !s.captureSuspended() }
func (s *screen) sessionKey() string     { return "" }

func (s *screen) startLifecycle(context.Context, context.CancelFunc) (func(), error) {
	return func() {}, nil
}

var (
	xpropID     = regexp.MustCompile(`window id # (0x[0-9a-fA-F]+)`)
	xpropString = regexp.MustCompile(`^(\w+)\([^)]*\) = (.*)$`)
)

// activeWindow reads the front window's title, class and process.
func (s *screen) activeWindow() (window, error) {
	out, err := exec.Command("xprop", "-root", "_NET_ACTIVE_WINDOW").Output()
	if err != nil {
		return window{}, err
	}
	m := xpropID.FindStringSubmatch(string(out))
	if m == nil || m[1] == "0x0" {
		return window{}, errors.New("no active window")
	}
	out, err = exec.Command("xprop", "-id", m[1], "_NET_WM_NAME", "WM_NAME", "WM_CLASS", "_NET_WM_PID").Output()
	if err != nil {
		return window{}, err
	}
	props := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if p := xpropString.FindStringSubmatch(strings.TrimSpace(line)); p != nil {
			props[p[1]] = p[2]
		}
	}
	var w window
	w.title = unquote(props["_NET_WM_NAME"])
	if w.title == "" {
		w.title = unquote(props["WM_NAME"])
	}
	class := ""
	if parts := strings.Split(props["WM_CLASS"], ","); len(parts) > 0 {
		class = unquote(strings.TrimSpace(parts[len(parts)-1]))
	}
	if pid, err := strconv.Atoi(strings.TrimSpace(props["_NET_WM_PID"])); err == nil {
		if comm, err := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid)); err == nil {
			w.app = strings.TrimSpace(string(comm))
		}
	}
	if w.app == "" {
		w.app = strings.ToLower(class)
	}
	if w.app == "" {
		return window{}, errors.New("unknown window")
	}
	w.appName = friendlyName(class, w.app)
	return w, nil
}

// unquote turns `"text"` from xprop into text.
func unquote(s string) string {
	s = strings.TrimSpace(s)
	if v, err := strconv.Unquote(s); err == nil {
		return v
	}
	return strings.Trim(s, `"`)
}

var knownApps = map[string]string{
	"code": "Visual Studio Code", "google-chrome": "Google Chrome", "chromium": "Chromium", "firefox": "Firefox",
	"brave-browser": "Brave", "microsoft-edge": "Microsoft Edge", "telegramdesktop": "Telegram", "slack": "Slack",
	"gnome-terminal-server": "Terminal", "gnome-terminal": "Terminal", "x-terminal-emulator": "Terminal",
	"nemo": "Files", "nautilus": "Files", "libreoffice": "LibreOffice", "thunderbird": "Thunderbird",
	"postman": "Postman", "pgadmin4": "pgAdmin 4", "jetbrains-idea": "IntelliJ IDEA", "zoom": "Zoom",
}

func friendlyName(class, process string) string {
	if n, ok := knownApps[strings.ToLower(class)]; ok {
		return n
	}
	if n, ok := knownApps[strings.ToLower(process)]; ok {
		return n
	}
	if class != "" {
		return class
	}
	return process
}

// screenshot saves the whole screen as a JPEG, scaled to maxWidth when it is not 0.
func (s *screen) screenshot(ctx context.Context, path string, maxWidth, quality int) error {
	pipeline := "ximagesrc num-buffers=1 use-damage=false ! videoconvert"
	if maxWidth > 0 {
		pipeline += fmt.Sprintf(" ! videoscale ! video/x-raw,width=%d,pixel-aspect-ratio=1/1", maxWidth)
	}
	pipeline += fmt.Sprintf(" ! jpegenc quality=%d ! filesink", quality)
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "gst-launch-1.0", gstArgs(pipeline, path)...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// record captures the screen at 1 frame per second (full resolution, VP8 WebM) for up to length,
// ending early and cleanly when ctx ends or stop() turns true. Constant-quality mode keeps text
// readable at about 9 MB per 10 minutes, since most of a screen stays still between frames.
func (s *screen) record(ctx context.Context, path string, length time.Duration, stop func() bool) error {
	frames := int(length.Seconds())
	pipeline := fmt.Sprintf("ximagesrc use-damage=false num-buffers=%d ! video/x-raw,framerate=1/1 ! videoconvert"+
		" ! vp8enc deadline=1 end-usage=cq cq-level=24 target-bitrate=2000000 keyframe-max-dist=30 cpu-used=8"+
		" ! webmmux ! filesink", frames)
	cmd := exec.Command("gst-launch-1.0", append([]string{"-e"}, gstArgs(pipeline, path)...)...)
	// Own process group: Ctrl+C or a service stop reaches only the agent, which then ends the
	// recording once and cleanly (a second interrupt would abort the file). If the agent itself
	// dies, the recorder goes too, instead of recording on next to its replacement.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if err := cmd.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	check := time.NewTicker(5 * time.Second)
	defer check.Stop()
	for {
		select {
		case err := <-done:
			return err
		case <-ctx.Done():
			cmd.Process.Signal(syscall.SIGINT) // -e: finish the file properly
			return keepIfWritten(path, waitOrKill(cmd, done))
		case <-check.C:
			if stop() {
				cmd.Process.Signal(syscall.SIGINT)
				return keepIfWritten(path, waitOrKill(cmd, done))
			}
		}
	}
}

// keepIfWritten accepts a recording stopped on request when a file was written.
func keepIfWritten(path string, err error) error {
	if info, statErr := os.Stat(path); statErr == nil && info.Size() > 0 {
		return nil
	}
	return err
}

func waitOrKill(cmd *exec.Cmd, done chan error) error {
	select {
	case err := <-done:
		return err
	case <-time.After(10 * time.Second):
		cmd.Process.Kill()
		return errors.New("recorder did not stop in time")
	}
}

// gstArgs turns a pipeline ending in "filesink" into gst-launch arguments, quoting the file
// path so it stays one value even with spaces in it.
func gstArgs(pipeline, path string) []string {
	return append(append([]string{"-q"}, strings.Fields(pipeline)...), `location="`+strings.ReplaceAll(path, `"`, `\"`)+`"`)
}

// watchInput calls key() for every key press and click() for every mouse button press
// (not scroll-wheel steps) anywhere on the screen, until ctx ends. It reads xinput's raw
// events, which say that a key was pressed but the agent never looks at which one.
func (s *screen) watchInput(ctx context.Context, key, click func()) error {
	if _, err := exec.LookPath("xinput"); err != nil {
		return errors.New("xinput is missing (sudo apt install xinput): key strokes and mouse clicks are not counted")
	}
	args := []string{"xinput", "test-xi2", "--root"}
	if _, err := exec.LookPath("stdbuf"); err == nil {
		args = append([]string{"stdbuf", "-oL"}, args...) // deliver each event as it happens
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	// Dies with the agent, so a restarted agent can listen for clicks again
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	sc := bufio.NewScanner(out)
	button := false // inside a RawButtonPress event, waiting for its "detail:" line
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.Contains(line, "(RawKeyPress)"):
			key()
		case strings.Contains(line, "(RawButtonPress)"):
			button = true
		case button && strings.HasPrefix(strings.TrimSpace(line), "detail:"):
			button = false
			// Buttons 4-7 are scroll-wheel steps, not clicks
			if n, err := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "detail:"))); err == nil && (n < 4 || n > 7) {
				click()
			}
		}
	}
	if err := cmd.Wait(); err != nil {
		if strings.Contains(stderr.String(), "BadAccess") {
			return errors.New("another program is already listening for clicks on this desktop (is a second HR Lens agent running?)")
		}
		return err
	}
	return nil
}

// sessionStart is when the person signed in to this desktop session (from systemd-logind).
func (s *screen) sessionStart() (time.Time, bool) {
	id := os.Getenv("XDG_SESSION_ID")
	if id == "" {
		return time.Time{}, false
	}
	// Prints "t <microseconds since 1970>"
	out, err := exec.Command("busctl", "get-property", "org.freedesktop.login1",
		"/org/freedesktop/login1/session/"+id, "org.freedesktop.login1.Session", "Timestamp").Output()
	if err != nil {
		return time.Time{}, false
	}
	fields := strings.Fields(string(out))
	if len(fields) != 2 {
		return time.Time{}, false
	}
	us, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || us <= 0 {
		return time.Time{}, false
	}
	return time.UnixMicro(us), true
}

// singleInstance holds a lock for as long as the agent runs, so a second copy in the same
// sign-in refuses to start (two would double-count, and X11 lets only one listen for clicks).
func singleInstance() (release func(), err error) {
	dir := os.Getenv("XDG_RUNTIME_DIR")
	if dir == "" {
		dir = os.TempDir()
	}
	f, err := os.OpenFile(fmt.Sprintf("%s/hrlens-agent-%d.lock", dir, os.Getuid()), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errors.New("the agent is already running for this sign-in (stop it with: pkill -f 'hrlens-agent run')")
	}
	return func() { f.Close() }, nil
}
