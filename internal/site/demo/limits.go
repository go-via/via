package demo

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-via/via"
)

// Limiter is a per-session token bucket. The zero value is not usable.
type Limiter struct {
	perMinute float64
	calls     atomic.Uint64
	buckets   sync.Map // session id -> *bucket
}

const (
	bucketIdle = 5 * time.Minute
	sweepEvery = 256
)

type bucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func NewLimiter(perMinute int) *Limiter {
	return &Limiter{perMinute: float64(perMinute)}
}

// Allow spends a token if the session has one. Visitors without a session
// share the "" bucket, so pages call shell.EnsureSession first.
func (l *Limiter) Allow(ctx *via.Ctx) bool {
	if l.calls.Add(1)%sweepEvery == 0 {
		l.sweep()
	}
	id := ctx.Session().ID()
	v, ok := l.buckets.Load(id)
	if !ok {
		v, _ = l.buckets.LoadOrStore(id, &bucket{tokens: l.perMinute, last: time.Now()})
	}
	b := v.(*bucket)

	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.tokens = min(l.perMinute, b.tokens+now.Sub(b.last).Minutes()*l.perMinute)
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// A bucket idle past bucketIdle is full again, so deleting it changes nothing.
func (l *Limiter) sweep() {
	cutoff := time.Now().Add(-bucketIdle)
	l.buckets.Range(func(k, v any) bool {
		b := v.(*bucket)
		b.mu.Lock()
		idle := b.last.Before(cutoff)
		b.mu.Unlock()
		if idle {
			l.buckets.Delete(k)
		}
		return true
	})
}
