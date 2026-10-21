package main

import (
	"context"
	"time"
)

func (s *screen) presenceChanges(previous *bool, now time.Time) []presence {
	locked := s.attendanceLocked()
	if locked == *previous {
		return nil
	}
	*previous = locked
	kind := "unlock"
	if locked {
		kind = "lock"
	}
	return []presence{{Kind: kind, At: now}}
}
func (s *screen) signedOut() bool { return true }
func (s *screen) watchInputReady(ctx context.Context, count func(int, int), ready func()) error {
	if ready != nil {
		ready()
	}
	return s.watchInput(ctx, func() { count(1, 0) }, func() { count(0, 1) })
}
