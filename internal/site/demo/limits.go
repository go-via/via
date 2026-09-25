package demo

import (
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-via/via"
)

// Limiter is a per-client token bucket, refilling at perMinute a minute and
// capped at one minute's worth. Build one with NewLimiter.
type Limiter struct {
	perMinute float64
	calls     atomic.Uint64
	buckets   sync.Map // client ip -> *bucket
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

// NewLimiter panics on a budget of zero or less: a limiter nobody can spend
// from is a wiring mistake, and it is cheaper to find at startup.
func NewLimiter(perMinute int) *Limiter {
	if perMinute <= 0 {
		panic("demo: NewLimiter: perMinute must be > 0, got " + strconv.Itoa(perMinute))
	}
	return &Limiter{perMinute: float64(perMinute)}
}

// Allow spends a token if the client has one. Buckets are keyed on the client
// IP, so a visitor cannot buy a fresh budget by dropping the session cookie.
func (l *Limiter) Allow(ctx *via.Ctx) bool {
	if l.calls.Add(1)%sweepEvery == 0 {
		l.sweep()
	}
	key := clientKey(ctx.Request())
	v, ok := l.buckets.Load(key)
	if !ok {
		v, _ = l.buckets.LoadOrStore(key, &bucket{tokens: l.perMinute, last: time.Now()})
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

// clientKey is the client IP. Caddy fronts the deployed site on the same host
// and by default replaces any X-Forwarded-For the client sent, so a loopback
// peer is trusted for the rightmost entry; any other peer is the client and the
// header is ignored. A trusted_proxies block in the Caddyfile would break this.
func clientKey(r *http.Request) string {
	if r == nil {
		return ""
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		for i := len(parts) - 1; i >= 0; i-- {
			// A stray comma would key every such request on one "" bucket.
			if e := strings.TrimSpace(parts[i]); e != "" {
				return e
			}
		}
	}
	return host
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
