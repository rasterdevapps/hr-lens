// Command agent is the HR Lens desktop agent. It runs on an employee's computer and sends
// the server what is on screen: the active application and window (activity), a screenshot
// every few minutes, continuous screen recording in 10-minute pieces, and live frames while
// a manager is watching.
//
//	hrlens-agent register -server http://localhost:8080 -email you@example.com
//	hrlens-agent run
//
// On Windows only the activity part is built so far (see WINDOWS.md); screen capture is Linux only.
//
// Nothing is captured while the screen is locked. Files waiting to be uploaded are kept in a
// spool folder and retried, so nothing is lost while the server is unreachable.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// version is set by cmd/release (-ldflags "-X main.version=..."); a plain go build is "dev",
// which never updates itself.
var version = "dev"

const (
	heartbeatEvery  = 5 * time.Second  // also how quickly live view starts
	sampleEvery     = 5 * time.Second  // how often the active window is checked
	flushEvery      = time.Minute      // how often activity is sent
	liveEvery       = 2 * time.Second  // live frame rate while watched
	recordingLength = 10 * time.Minute // one recording piece
	uploadRetry     = time.Minute
)

type config struct {
	Server     string `json:"server"`
	Token      string `json:"token"`
	DeviceName string `json:"deviceName"`
	UserName   string `json:"userName"`
}

func main() {
	if handled, err := runNativeHost(os.Args[1:]); handled {
		if err != nil {
			fmt.Fprintln(os.Stderr, "HR Lens browser host:", err)
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewTextHandler(logOutput(), nil))
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "register":
		err = register(os.Args[2:])
	case "run":
		if os.Getenv(workerEnv) == "" {
			err = supervise(log)
			break
		}
		// The worker: its exit code tells supervise what to do next
		switch err = run(log); {
		case errors.Is(err, errRestart):
			os.Exit(exitRestart)
		case err != nil:
			log.Error("the agent cannot run", "error", err)
			os.Exit(exitFatal)
		}
	case "status":
		err = status()
	case "install":
		err = install()
	case "uninstall":
		err = uninstall()
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		if os.Args[1] == "run" {
			log.Error("agent stopped", "error", err)
		} else {
			fmt.Fprintln(os.Stderr, "hrlens-agent:", err)
		}
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage:
  hrlens-agent register -server URL -email EMAIL   sign this computer in (asks for the password)
  hrlens-agent run                                 capture and upload until stopped
  hrlens-agent status                              show which account this computer uses
  hrlens-agent install                             start automatically at every login
  hrlens-agent uninstall                           stop starting at login`)
}

// ---------- Config ----------

func configDir() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "hrlens-agent"), nil
}

func loadConfig() (config, string, error) {
	dir, err := configDir()
	if err != nil {
		return config{}, "", err
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if errors.Is(err, os.ErrNotExist) {
		return config{}, dir, errors.New("this computer is not registered yet: run  hrlens-agent register")
	}
	if err != nil {
		return config{}, dir, err
	}
	var c config
	if err := json.Unmarshal(data, &c); err != nil {
		return config{}, dir, fmt.Errorf("config.json: %w", err)
	}
	return c, dir, nil
}

func saveConfig(c config) error {
	dir, err := configDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(c, "", "  ")
	return os.WriteFile(filepath.Join(dir, "config.json"), data, 0o600) // holds the device token
}

// ---------- register / status ----------

func register(args []string) error {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	server := fs.String("server", "http://localhost:8080", "HR Lens server `URL`")
	email := fs.String("email", "", "your HR Lens `email`")
	name := fs.String("name", "", "name for this computer (default: its host name)")
	fs.Parse(args)
	if *email == "" {
		return errors.New("-email is required")
	}
	if *name == "" {
		*name, _ = os.Hostname()
	}
	password := os.Getenv("HRLENS_PASSWORD")
	if password == "" {
		var err error
		if password, err = readPassword("Password for " + *email + ": "); err != nil {
			return err
		}
	}

	body, _ := json.Marshal(map[string]string{
		"email": *email, "password": password, "deviceName": *name,
		"os": runtime.GOOS + "/" + runtime.GOARCH, "agentVersion": version,
	})
	base := strings.TrimRight(*server, "/")
	resp, err := http.Post(base+"/api/agent/register", "application/json", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("cannot reach %s: %w", base, err)
	}
	defer resp.Body.Close()
	var out struct {
		Token string `json:"token"`
		Name  string `json:"name"`
		Error string `json:"error"`
	}
	json.NewDecoder(resp.Body).Decode(&out)
	if resp.StatusCode != http.StatusCreated {
		return fmt.Errorf("registration refused: %s", cmpStr(out.Error, resp.Status))
	}
	if err := saveConfig(config{Server: base, Token: out.Token, DeviceName: *name, UserName: out.Name}); err != nil {
		return err
	}
	fmt.Printf("Registered %q for %s. Start capturing with:  hrlens-agent run\n", *name, out.Name)
	return nil
}

// readPassword reads a line without echoing it.
func readPassword(prompt string) (string, error) {
	fmt.Fprint(os.Stderr, prompt)
	if restore := echoOff(); restore != nil {
		defer func() {
			restore()
			fmt.Fprintln(os.Stderr)
		}()
	}
	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return "", errors.New("no password entered")
	}
	return strings.TrimRight(line, "\r\n"), nil
}

func status() error {
	c, dir, err := loadConfig()
	if err != nil {
		return err
	}
	fmt.Printf("Server:   %s\nAccount:  %s\nComputer: %s\nConfig:   %s\n", c.Server, c.UserName, c.DeviceName, dir)
	return nil
}

// ---------- run ----------

type agent struct {
	cfg    config
	log    *slog.Logger
	http   *http.Client
	spool  string // files waiting to upload
	screen *screen

	mu        sync.Mutex
	live      bool
	shotEvery time.Duration
	current   window // front window at the last sample
	events    []event
	input     map[time.Time]*inputMinute // key presses and clicks per minute, not sent yet
	presence  []presence                 // sign-in, lock, unlock, sign-out, not sent yet
	tabs      *tabs                      // websites reported by the browser extension
	restart   func()                     // stops the agent so supervise starts it again (after an update)
}

type presence struct {
	Kind string    `json:"kind"`
	At   time.Time `json:"at"`
}

func (a *agent) note(kind string, at time.Time) {
	a.mu.Lock()
	a.presence = append(a.presence, presence{Kind: kind, At: at})
	a.mu.Unlock()
}

type inputMinute struct {
	Minute time.Time `json:"minute"`
	Keys   int       `json:"keys"`
	Clicks int       `json:"clicks"`
}

type event struct {
	StartedAt time.Time `json:"startedAt"`
	Seconds   int       `json:"seconds"`
	App       string    `json:"app"`
	AppName   string    `json:"appName"`
	Title     string    `json:"title"`
	Domain    string    `json:"domain,omitempty"`
}

func run(log *slog.Logger) error {
	c, dir, err := loadConfig()
	if err != nil {
		return err
	}
	scr, err := newScreen()
	if err != nil {
		return err
	}
	release, err := singleInstance()
	if err != nil {
		return err
	}
	defer release()
	a := &agent{
		cfg: c, log: log, http: &http.Client{Timeout: 2 * time.Minute},
		spool: filepath.Join(dir, "spool"), screen: scr, shotEvery: 5 * time.Minute,
		input: map[time.Time]*inputMinute{}, tabs: newTabs(),
	}
	if err := os.MkdirAll(a.spool, 0o700); err != nil {
		return err
	}
	// Pieces cut off by a crash or power loss cannot be played: drop them
	if parts, _ := filepath.Glob(filepath.Join(a.spool, "*.part")); len(parts) > 0 {
		for _, p := range parts {
			os.Remove(p)
		}
	}

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, restart := context.WithCancel(sigCtx)
	defer restart()
	a.restart = restart

	// The day's start time is the desktop sign-in; the agent starts at sign-in, so its own
	// start time is the fallback. The server ignores the same sign-in sent again.
	signedIn, ok := scr.sessionStart()
	if !ok {
		signedIn = time.Now()
	}
	a.note("login", signedIn)
	if scr.locked() {
		a.note("lock", time.Now())
	}

	log.Info("agent running", "server", c.Server, "account", c.UserName, "computer", c.DeviceName, "version", version)

	var wg sync.WaitGroup
	for _, loop := range []func(context.Context){a.heartbeats, a.activity, a.inputCounts, a.screenshots, a.recordings, a.liveFrames, a.uploads, a.serveTabs, a.updates} {
		wg.Add(1)
		go func(f func(context.Context)) { defer wg.Done(); f(ctx) }(loop)
	}
	wg.Wait()
	updating := sigCtx.Err() == nil // stopped by a.restart, not by a signal
	if !updating {
		// Stopping means signing out or shutting down
		a.note("logout", time.Now())
	}
	// Send what is left, including the minute in progress
	a.flushInput(context.Background(), true)
	a.flushActivity(context.Background())
	if updating {
		log.Info("agent stopped to start the new version")
		return errRestart
	}
	log.Info("agent stopped")
	return nil
}

// every runs f now and then every d until ctx ends.
func every(ctx context.Context, d time.Duration, f func()) {
	t := time.NewTicker(d)
	defer t.Stop()
	for {
		f()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (a *agent) heartbeats(ctx context.Context) {
	every(ctx, heartbeatEvery, func() {
		var out struct {
			Live            bool `json:"live"`
			ScreenshotEvery int  `json:"screenshotEvery"`
		}
		if err := a.postJSON(ctx, "/api/agent/heartbeat", nil, &out); err != nil {
			a.log.Warn("heartbeat failed", "error", err)
			return
		}
		a.mu.Lock()
		if out.Live != a.live {
			a.log.Info("live view", "watching", out.Live)
		}
		a.live = out.Live
		if out.ScreenshotEvery >= 30 {
			a.shotEvery = time.Duration(out.ScreenshotEvery) * time.Second
		}
		a.mu.Unlock()
	})
}

// activity samples the front window and turns unbroken stretches into events.
func (a *agent) activity(ctx context.Context) {
	var open *event
	lastFlush := time.Now()
	wasLocked := a.screen.locked()
	closeOpen := func(now time.Time) {
		if open == nil {
			return
		}
		open.Seconds = int(now.Sub(open.StartedAt).Round(time.Second).Seconds())
		if open.Seconds > 0 {
			a.mu.Lock()
			a.events = append(a.events, *open)
			if len(a.events) > 5000 { // server unreachable for hours: keep the newest
				a.events = a.events[len(a.events)-5000:]
			}
			a.mu.Unlock()
		}
		open = nil
	}
	every(ctx, sampleEvery, func() {
		now := time.Now()
		locked := a.screen.locked()
		if locked != wasLocked {
			a.note(map[bool]string{true: "lock", false: "unlock"}[locked], now)
			wasLocked = locked
		}
		win, err := a.screen.activeWindow()
		if err != nil || locked {
			closeOpen(now) // locked or nothing in front: not working time
		} else {
			a.mu.Lock()
			a.current = win
			a.mu.Unlock()
			domain := ""
			if isBrowser(win.app) {
				domain = a.tabs.host(win.title)
			}
			if open == nil || open.App != win.app || open.Title != win.title || open.Domain != domain {
				closeOpen(now)
				open = &event{StartedAt: now, App: win.app, AppName: win.appName, Title: win.title, Domain: domain}
			}
		}
		if now.Sub(lastFlush) >= flushEvery {
			// Split long stretches at each flush so the server sees activity as it happens
			if open != nil {
				cont := *open
				closeOpen(now)
				cont.StartedAt = now
				open = &cont
			}
			a.flushActivity(ctx)
			lastFlush = now
		}
	})
	closeOpen(time.Now())
}

// inputCounts counts key presses and mouse clicks per minute (never which keys), except while
// the screen is locked, so a lock-screen password is not counted.
func (a *agent) inputCounts(ctx context.Context) {
	count := func(keys, clicks int) func() {
		return func() {
			if a.screen.locked() {
				return
			}
			minute := time.Now().Truncate(time.Minute)
			a.mu.Lock()
			m := a.input[minute]
			if m == nil {
				m = &inputMinute{Minute: minute}
				a.input[minute] = m
			}
			m.Keys += keys
			m.Clicks += clicks
			a.mu.Unlock()
		}
	}
	for ctx.Err() == nil {
		err := a.screen.watchInput(ctx, count(1, 0), count(0, 1))
		if ctx.Err() != nil {
			return
		}
		if errors.Is(err, errors.ErrUnsupported) {
			a.log.Info("key and click counts are not available on this computer")
			return
		}
		a.log.Warn("input counting stopped; retrying in a minute", "error", err)
		sleep(ctx, time.Minute)
	}
}

// flushInput sends the counts of finished minutes, or of every minute when the agent is stopping.
func (a *agent) flushInput(ctx context.Context, all bool) {
	now := time.Now().Truncate(time.Minute)
	if all {
		now = now.Add(time.Minute)
	}
	a.mu.Lock()
	batch := []inputMinute{}
	for minute, m := range a.input {
		if minute.Before(now) && len(batch) < 1440 {
			batch = append(batch, *m)
		}
	}
	a.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if err := a.postJSON(ctx, "/api/agent/input", map[string]any{"minutes": batch}, nil); err != nil {
		a.log.Warn("sending key and click counts failed; will retry", "minutes", len(batch), "error", err)
		return
	}
	a.mu.Lock()
	for _, m := range batch {
		delete(a.input, m.Minute)
	}
	a.mu.Unlock()
}

func (a *agent) flushPresence(ctx context.Context) {
	a.mu.Lock()
	batch := append([]presence(nil), a.presence...)
	a.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if err := a.postJSON(ctx, "/api/agent/presence", map[string]any{"events": batch}, nil); err != nil {
		a.log.Warn("sending sign-in / lock times failed; will retry", "events", len(batch), "error", err)
		return
	}
	a.mu.Lock()
	a.presence = a.presence[len(batch):]
	a.mu.Unlock()
}

func (a *agent) flushActivity(ctx context.Context) {
	a.flushPresence(ctx)
	a.flushInput(ctx, false)
	a.mu.Lock()
	batch := a.events
	if len(batch) > 500 {
		batch = batch[:500]
	}
	a.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if err := a.postJSON(ctx, "/api/agent/activity", map[string]any{"events": batch}, nil); err != nil {
		a.log.Warn("sending activity failed; will retry", "events", len(batch), "error", err)
		return
	}
	a.mu.Lock()
	a.events = a.events[len(batch):]
	a.mu.Unlock()
}

func (a *agent) frontWindow() window {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.current
}

// screenshots saves one into the spool folder every few minutes; uploads() sends it.
func (a *agent) screenshots(ctx context.Context) {
	if !canCapture {
		return
	}
	next := time.Now()
	every(ctx, 5*time.Second, func() {
		if time.Now().Before(next) {
			return
		}
		a.mu.Lock()
		next = time.Now().Add(a.shotEvery)
		a.mu.Unlock()
		// No front window yet means the desktop is still starting: the screen is blank
		if a.screen.locked() || a.frontWindow().appName == "" {
			return
		}
		at := time.Now()
		path := filepath.Join(a.spool, "screenshot-"+at.UTC().Format("20060102T150405")+".jpg")
		if err := a.screen.screenshot(ctx, path, 0, 60); err != nil {
			a.log.Warn("screenshot failed", "error", err)
			return
		}
		win := a.frontWindow()
		writeMeta(path, map[string]string{"takenAt": at.Format(time.RFC3339), "app": win.appName, "title": win.title})
	})
}

// recordings records the screen continuously in pieces; each finished piece goes to the spool.
func (a *agent) recordings(ctx context.Context) {
	if !canCapture {
		return
	}
	for ctx.Err() == nil {
		if a.screen.locked() {
			sleep(ctx, 10*time.Second)
			continue
		}
		start := time.Now()
		path := filepath.Join(a.spool, "recording-"+start.UTC().Format("20060102T150405")+".webm")
		part := path + ".part" // not uploaded until finished
		err := a.screen.record(ctx, part, pieceLength(), a.screen.locked)
		took := time.Since(start)
		if err != nil {
			os.Remove(part)
			if ctx.Err() == nil {
				a.log.Warn("recording failed; retrying shortly", "error", err)
				sleep(ctx, 30*time.Second)
			}
			continue
		}
		if took < 5*time.Second { // too short to be worth keeping
			os.Remove(part)
			continue
		}
		win := a.frontWindow()
		writeMeta(path, map[string]string{
			"startedAt": start.Format(time.RFC3339), "seconds": fmt.Sprint(int(took.Seconds())),
			"app": win.appName, "title": win.title,
		})
		os.Rename(part, path)
	}
}

// liveFrames sends a small screenshot every 2 seconds while a manager is watching.
func (a *agent) liveFrames(ctx context.Context) {
	if !canCapture {
		return
	}
	tmp := filepath.Join(a.spool, "live.jpg.part")
	every(ctx, liveEvery, func() {
		a.mu.Lock()
		live := a.live
		a.mu.Unlock()
		if !live || a.screen.locked() {
			return
		}
		if err := a.screen.screenshot(ctx, tmp, 1280, 50); err != nil {
			a.log.Warn("live frame failed", "error", err)
			return
		}
		data, err := os.ReadFile(tmp)
		os.Remove(tmp)
		if err != nil {
			return
		}
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.Server+"/api/agent/live", bytes.NewReader(data))
		req.Header.Set("Content-Type", "image/jpeg")
		if resp, err := a.do(req); err == nil {
			resp.Body.Close()
		}
	})
}

// uploads sends spooled screenshots and recordings, oldest first, retrying on failure.
func (a *agent) uploads(ctx context.Context) {
	for ctx.Err() == nil {
		files, _ := filepath.Glob(filepath.Join(a.spool, "*"))
		sort.Strings(files)
		sent := false
		for _, f := range files {
			if strings.HasSuffix(f, ".uploaded") {
				if _, err := os.Stat(f); errors.Is(err, os.ErrNotExist) {
					continue
				}
				base := strings.TrimSuffix(f, ".uploaded")
				if err := removeUpload(base); err != nil {
					a.log.Warn("upload cleanup failed; will retry", "file", filepath.Base(base), "error", err)
					break
				}
				sent = true
				continue
			}
			ext := filepath.Ext(f)
			if ext != ".jpg" && ext != ".webm" {
				continue
			}
			if err := a.upload(ctx, f); err != nil {
				a.log.Warn("upload failed; will retry", "file", filepath.Base(f), "error", err)
				break
			}
			sent = true
		}
		if !sent {
			sleep(ctx, uploadRetry/4)
		}
	}
}

func (a *agent) upload(ctx context.Context, path string) error {
	if uploaded, err := hasUploadedReceipt(path); err != nil {
		return err
	} else if uploaded {
		return removeUpload(path)
	}

	meta, err := loadMeta(path)
	if err != nil {
		return err
	}
	endpoint, ctype := "/api/agent/screenshots", "image/jpeg"
	if strings.HasSuffix(path, ".webm") {
		endpoint, ctype = "/api/agent/recordings", "video/webm"
	}
	file, err := os.Open(path)
	if err != nil {
		return err
	}

	// Stream the multipart body instead of loading a whole recording into memory
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.Server+endpoint, pr)
	if err != nil {
		_ = file.Close()
		return err
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())

	writeDone := make(chan error, 1)
	go func() {
		var err error
		keys := make([]string, 0, len(meta))
		for k := range meta {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if err = mw.WriteField(k, meta[k]); err != nil {
				break
			}
		}
		if err == nil {
			h := textproto.MIMEHeader{}
			h.Set("Content-Disposition", fmt.Sprintf(`form-data; name="file"; filename=%q`, filepath.Base(path)))
			h.Set("Content-Type", ctype)
			var part io.Writer
			if part, err = mw.CreatePart(h); err == nil {
				_, err = io.Copy(part, file)
			}
		}
		if err == nil {
			err = mw.Close()
		}
		_ = pw.CloseWithError(err)
		writeDone <- err
	}()

	resp, doErr := a.do(req)
	_ = pr.Close()
	writeErr := <-writeDone
	closeErr := file.Close()
	if doErr != nil {
		if resp != nil {
			_ = resp.Body.Close()
		}
		return doErr
	}
	if writeErr != nil && !errors.Is(writeErr, io.ErrClosedPipe) {
		_ = resp.Body.Close()
		return writeErr
	}
	if closeErr != nil {
		_ = resp.Body.Close()
		return closeErr
	}

	switch resp.StatusCode {
	case http.StatusBadRequest:
		// The server will never accept this file: drop it instead of retrying forever
		msg, readErr := io.ReadAll(io.LimitReader(resp.Body, 300))
		bodyCloseErr := resp.Body.Close()
		if readErr != nil {
			return readErr
		}
		if bodyCloseErr != nil {
			return bodyCloseErr
		}
		a.log.Error("server rejected file; deleting it", "file", filepath.Base(path), "reason", strings.TrimSpace(string(msg)))
		return removeUpload(path)
	case http.StatusCreated:
		if err := resp.Body.Close(); err != nil {
			return err
		}
		if err := writeUploadedReceipt(path); err != nil {
			return err
		}
		return removeUpload(path)
	default:
		status := resp.Status
		_ = resp.Body.Close()
		return fmt.Errorf("server answered %s", status)
	}
}

// ---------- HTTP ----------

func (a *agent) do(req *http.Request) (*http.Response, error) {
	req.Header.Set("Authorization", "Bearer "+a.cfg.Token)
	req.Header.Set("User-Agent", "hrlens-agent/"+version)
	resp, err := a.http.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusUnauthorized {
		resp.Body.Close()
		return nil, errors.New("this computer's registration was removed or the account is inactive: run  hrlens-agent register  again")
	}
	return resp, nil
}

func (a *agent) postJSON(ctx context.Context, path string, body, out any) error {
	var r io.Reader = http.NoBody
	if body != nil {
		data, _ := json.Marshal(body)
		r = bytes.NewReader(data)
	}
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, a.cfg.Server+path, r)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("%s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	return nil
}

// ---------- Helpers ----------

func writeMeta(path string, meta map[string]string) {
	data, _ := json.Marshal(meta)
	os.WriteFile(path+".json", data, 0o600)
}

func loadMeta(path string) (map[string]string, error) {
	meta := map[string]string{}
	data, err := os.ReadFile(path + ".json")
	if errors.Is(err, os.ErrNotExist) {
		return meta, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return meta, nil
}

func readMeta(path string) map[string]string {
	meta, _ := loadMeta(path)
	return meta
}

func uploadReceiptPath(path string) string {
	return path + ".uploaded"
}

func hasUploadedReceipt(path string) (bool, error) {
	_, err := os.Stat(uploadReceiptPath(path))
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

func writeUploadedReceipt(path string) error {
	f, err := os.OpenFile(uploadReceiptPath(path), os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.WriteString("uploaded\n"); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

func removeUpload(path string) error {
	if err := removeIfExists(path); err != nil {
		return err
	}
	if err := removeIfExists(path + ".json"); err != nil {
		return err
	}
	return removeIfExists(uploadReceiptPath(path))
}

// pieceLength is how long each recording piece runs; HRLENS_RECORDING_SECONDS shortens it for testing.
func pieceLength() time.Duration {
	if v, err := strconv.Atoi(os.Getenv("HRLENS_RECORDING_SECONDS")); err == nil && v >= 10 {
		return time.Duration(v) * time.Second
	}
	return recordingLength
}

func sleep(ctx context.Context, d time.Duration) {
	select {
	case <-ctx.Done():
	case <-time.After(d):
	}
}

func cmpStr(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
