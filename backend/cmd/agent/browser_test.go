package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTabHost(t *testing.T) {
	tb := newTabs()
	tb.put("Inbox (3) - Gmail", "mail.google.com")
	tb.put("Inbox", "outlook.office.com")
	tb.put("HR Lens", "localhost")
	cases := map[string]string{
		"Inbox (3) - Gmail - Google Chrome": "mail.google.com", // longest matching title wins
		"Inbox - Google Chrome":             "outlook.office.com",
		"HR Lens - Google Chrome":           "localhost",
		"New Tab - Google Chrome":           "",
	}
	for window, want := range cases {
		if got := tb.host(window); got != want {
			t.Errorf("host(%q) = %q, want %q", window, got, want)
		}
	}
	if !isBrowser("chrome") || isBrowser("code") {
		t.Error("isBrowser")
	}
}

func TestTabHandler(t *testing.T) {
	a := &agent{log: slog.New(slog.NewTextHandler(io.Discard, nil)), tabs: newTabs()}
	srv := httptest.NewServer(a.tabHandler())
	t.Cleanup(srv.Close)

	post := func(origin string) int {
		req, _ := http.NewRequest("POST", srv.URL+"/tab", strings.NewReader(`{"title":"Pull request #42 · GitHub","host":"github.com"}`))
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	if code := post("https://evil.example"); code != http.StatusForbidden {
		t.Errorf("a web page was accepted: %d", code)
	}
	if code := post(""); code != http.StatusForbidden {
		t.Errorf("no origin was accepted: %d", code)
	}
	if code := post("chrome-extension://jobmeopcpnhamneamceglchglicfhdlk"); code != http.StatusNoContent {
		t.Errorf("the extension was refused: %d", code)
	}
	if got := a.tabs.host("Pull request #42 · GitHub - Google Chrome"); got != "github.com" {
		t.Errorf("host = %q", got)
	}
}
