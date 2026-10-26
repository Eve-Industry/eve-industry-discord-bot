package admin

import (
	"sync"
	"time"
)

// limiter counts failures per key over a sliding window, in memory. The bot
// runs as one process, so nothing needs sharing.
type limiter struct {
	max    int
	window time.Duration

	mu   sync.Mutex
	hits map[string][]time.Time
}

func newLimiter(max int, window time.Duration) *limiter {
	return &limiter{max: max, window: window, hits: map[string][]time.Time{}}
}

// blocked reports whether key has failed max times within the window, and
// when the oldest of those failures ages out.
func (l *limiter) blocked(key string, now time.Time) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := l.prune(key, now)
	if len(h) < l.max {
		return false, 0
	}
	return true, h[0].Add(l.window).Sub(now)
}

// fail records a failure for key and returns how many are in the window.
func (l *limiter) fail(key string, now time.Time) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	h := append(l.prune(key, now), now)
	l.hits[key] = h
	return len(h)
}

func (l *limiter) reset(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.hits, key)
}

func (l *limiter) prune(key string, now time.Time) []time.Time {
	h := l.hits[key]
	i := 0
	for i < len(h) && now.Sub(h[i]) >= l.window {
		i++
	}
	h = h[i:]
	if len(h) == 0 {
		delete(l.hits, key)
		return nil
	}
	l.hits[key] = h
	return h
}
