package main

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// The HR Lens browser extension tells the agent which website each tab shows: it posts the
// front tab's title and host name (never the full address) to this address on the same
// computer. The agent matches browser window titles against those tab titles.
const tabListenAddr = "127.0.0.1:47123"

var browserProcesses = map[string]bool{
	"chrome": true, "google-chrome": true, "chromium": true, "chromium-browser": true,
	"brave": true, "brave-browser": true, "msedge": true, "microsoft-edge": true, "opera": true, "vivaldi-bin": true, "vivaldi": true,
}

func isBrowser(process string) bool { return browserProcesses[strings.ToLower(process)] }

type tabInfo struct {
	host string
	at   time.Time
}

// tabs remembers recently reported tabs by title.
type tabs struct {
	mu     sync.Mutex
	byName map[string]tabInfo
}

func newTabs() *tabs { return &tabs{byName: map[string]tabInfo{}} }

func (t *tabs) put(title, host string) {
	title = strings.TrimSpace(title)
	if title == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.byName[title] = tabInfo{host: host, at: time.Now()}
	if len(t.byName) > 300 { // forget the oldest
		var oldest string
		for k, v := range t.byName {
			if oldest == "" || v.at.Before(t.byName[oldest].at) {
				oldest = k
			}
		}
		delete(t.byName, oldest)
	}
}

// host finds the website for a browser window title such as "Inbox (3) - Gmail - Google Chrome":
// the longest reported tab title the window title starts with. "" when unknown.
func (t *tabs) host(windowTitle string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	best, bestLen := "", 0
	for title, info := range t.byName {
		if len(title) > bestLen && strings.HasPrefix(windowTitle, title) {
			best, bestLen = info.host, len(title)
		}
	}
	return best
}

// serveTabs accepts reports from the extension until ctx ends. Only browser extensions on
// this computer can reach it.
func (a *agent) serveTabs(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /tab", func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		if !strings.HasPrefix(origin, "chrome-extension://") && !strings.HasPrefix(origin, "moz-extension://") {
			http.Error(w, "forbidden", http.StatusForbidden)
			return
		}
		var body struct {
			Title string `json:"title"`
			Host  string `json:"host"`
		}
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 8<<10)).Decode(&body); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		a.tabs.put(body.Title, strings.ToLower(strings.TrimSpace(body.Host)))
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hrlens-agent " + version))
	})
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	ln, err := net.Listen("tcp", tabListenAddr)
	if err != nil {
		a.log.Warn("browser extension link unavailable: websites will not be recorded", "error", err)
		return
	}
	go func() {
		<-ctx.Done()
		srv.Close()
	}()
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		a.log.Warn("browser extension link stopped", "error", err)
	}
}
