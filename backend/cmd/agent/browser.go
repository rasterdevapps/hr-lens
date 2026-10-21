package main

import (
	"encoding/json"
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
		if time.Since(info.at) < 3*time.Minute && len(title) > bestLen && strings.HasPrefix(windowTitle, title) {
			best, bestLen = info.host, len(title)
		}
	}
	return best
}

// tabHandler handles browser reports independently of the production listener so tests
// can use an isolated ephemeral port without contacting a running desktop agent.
func (a *agent) tabHandler() http.Handler {
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
	return mux
}

// recent reports whether the optional extension has reached this session recently.
func (t *tabs) recent() bool {
	if t == nil {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for _, info := range t.byName {
		if time.Since(info.at) < 3*time.Minute {
			return true
		}
	}
	return false
}
