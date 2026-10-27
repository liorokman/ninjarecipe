package httpx

import (
	"math"
	"sync"
	"time"
)

// Limiter is an in-memory token bucket per key.
type Limiter struct {
	mu      sync.Mutex
	rate    float64 // tokens per second
	burst   float64
	buckets map[string]*bucket
	now     func() time.Time
}

type bucket struct {
	tokens float64
	last   time.Time
}

// NewLimiter allows perMinute sustained requests with the given burst.
func NewLimiter(perMinute int, burst int) *Limiter {
	return &Limiter{rate: float64(perMinute) / 60, burst: float64(burst), buckets: map[string]*bucket{}, now: time.Now}
}

// SetClock overrides time (tests).
func (l *Limiter) SetClock(f func() time.Time) { l.now = f }

// Allow consumes a token; when denied it returns the seconds to wait.
func (l *Limiter) Allow(key string) (ok bool, retryAfter int) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	b := l.buckets[key]
	if b == nil {
		b = &bucket{tokens: l.burst, last: now}
		l.buckets[key] = b
	}
	b.tokens = math.Min(l.burst, b.tokens+now.Sub(b.last).Seconds()*l.rate)
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true, 0
	}
	wait := (1 - b.tokens) / l.rate
	return false, int(math.Ceil(wait))
}
