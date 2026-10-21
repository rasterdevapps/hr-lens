package auth

import (
	"sync"
	"time"
)

// Limiter blocks a key (such as IP + email) after too many failed sign-ins.
// It is in-memory, so it resets on restart and is per server instance.
type Limiter struct {
	max    int
	window time.Duration
	now    func() time.Time

	mu       sync.Mutex
	failures map[string][]time.Time
}

func NewLimiter(max int, window time.Duration) *Limiter {
	return &Limiter{max: max, window: window, now: time.Now, failures: map[string][]time.Time{}}
}

// Allowed reports whether key may try again, and if not, how long to wait.
func (l *Limiter) Allowed(key string) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	recent := l.prune(key)
	if len(recent) < l.max {
		return true, 0
	}
	return false, l.window - l.now().Sub(recent[0])
}

func (l *Limiter) Fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures[key] = append(l.prune(key), l.now())
}

func (l *Limiter) Reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.failures, key)
}

func (l *Limiter) prune(key string) []time.Time {
	cutoff := l.now().Add(-l.window)
	list := l.failures[key]
	for len(list) > 0 && list[0].Before(cutoff) {
		list = list[1:]
	}
	if len(list) == 0 {
		delete(l.failures, key)
	} else {
		l.failures[key] = list
	}
	return list
}
