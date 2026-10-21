package main

import "time"

func (s *screen) presenceChanges(_ *bool, _ time.Time) []presence {
	return s.state.takePresenceTransitions()
}
func (s *screen) signedOut() bool {
	s.state.mu.RLock()
	defer s.state.mu.RUnlock()
	return s.state.signedOut
}
