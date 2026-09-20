package demo

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-via/via"
)

// Limiter is a per-session token bucket over the demos that mutate shared
// state. The zero value is not usable; call NewLimiter.
type Limiter struct {
	perMinute float64
	calls     atomic.Uint64
	buckets   sync.Map // session id -> *bucket
}

const (
	// A bucket idle this long has refilled completely, so it says the same
	// thing as no bucket at all.
	bucketIdle = 5 * time.Minute
	sweepEvery = 256
)

type bucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

// NewLimiter allows perMinute actions per session, bursting up to the same
// number.
func NewLimiter(perMinute int) *Limiter {
	return &Limiter{perMinute: float64(perMinute)}
}

// Allow reports whether this session may act now, spending a token if so. An
// anonymous visitor has no session id yet, so every such caller shares one
// bucket — a demo that mutates shared state should Put something in the
// session first.
func (l *Limiter) Allow(ctx *via.Ctx) bool {
	// sync.Map never shrinks on its own and the key is a session id, so
	// without this the map grows with every visitor for the life of the
	// process.
	if l.calls.Add(1)%sweepEvery == 0 {
		l.sweep()
	}
	v, _ := l.buckets.LoadOrStore(ctx.Session().ID(), &bucket{tokens: l.perMinute, last: time.Now()})
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

// sweep drops buckets nobody has spent from lately. A bucket refilled between
// the read and the Delete is re-created full on its owner's next call.
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

// Reset runs fn on every tick for the life of the process. Shared demo state
// is wiped this way so one visitor's mess does not greet the next.
func Reset(every time.Duration, fn func()) {
	go func() {
		for range time.Tick(every) {
			fn()
		}
	}()
}
