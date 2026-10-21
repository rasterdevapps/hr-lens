//go:build linux

package main

import (
	"context"
	"errors"
	"net"
	"net/http"
	"time"
)

// serveTabs accepts reports from the extension until ctx ends.
func (a *agent) serveTabs(ctx context.Context) {
	srv := &http.Server{Handler: a.tabHandler(), ReadHeaderTimeout: 5 * time.Second}
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

func runNativeHost([]string) (bool, error) { return false, nil }
