package main

import (
	"context"
	"syscall"
)

func (a *agent) initializeCapabilities(ctx context.Context) {
	available := true
	for _, proc := range []*syscall.LazyProc{setThreadDPI, getDC, releaseDC, getSystemMetrics, createCompatibleDC, createCompatibleBitmap, selectObject, deleteObject, deleteDC, bitBlt, getDIBits} {
		if err := proc.Find(); err != nil {
			available = false
			a.log.Warn("Windows capture API unavailable", "error", err)
			break
		}
	}
	a.setScreenshotsCapability(available)
	a.setLiveScreenCapability(available)
	path, err := probeFFmpegLibvpx(ctx, "")
	if err != nil {
		a.log.Info("screen recording unavailable; install managed FFmpeg with libvpx and restart", "error", err)
	} else {
		a.screen.ffmpegPath = path
	}
	a.setRecordingsCapability(available && err == nil)
	// InputCounts becomes true only after both native hooks install successfully.
}
