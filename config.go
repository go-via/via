package via

import (
	"log"
	"sync"
	"time"

	"github.com/go-via/via/h"
)

// config holds Handler's optional settings. The zero set is the dev-friendly
// default: the action endpoint accepts requests from any origin, and production
// opts into enforcement with WithTrustedOrigin.
type config struct {
	trustedOrigins map[string]bool
	sessionKey     []byte
	sessionTTL     time.Duration
	sessionCookie  string
	sessionSecure  bool
	sessionStore   SessionStore
	sessionTimeout time.Duration
	head           Head
	errorPage      func(*Ctx, PageError) h.H
	maxSSEConn     int
	pinnedDeadline time.Duration
}

// sseHeartbeat is the keepalive cadence. Fixed, never configurable: a failed
// keepalive write is the only in-band detector of a peer that vanished without
// a FIN, so the stream goroutine and its timers would leak without it.
const sseHeartbeat = 25 * time.Second

// sseWriteTimeout caps a single frame write, so a stalled peer can't pin the
// child's goroutine. Fixed: disabling it would let one pin a net/http goroutine
// per click, since an action POST waits behind this same deadline.
const sseWriteTimeout = 10 * time.Second

// defaultMaxSSEConn caps concurrent live SSE streams per Router; past it a
// connect is refused 503. See WithMaxSSEConn.
const defaultMaxSSEConn = 10_000

// defaultPinnedDeadline is how long a dispatch waits for the child goroutine
// before declaring it pinned. See WithPinnedDeadline.
const defaultPinnedDeadline = 5 * time.Second

// Option configures a Handler or a NewRouter. On a Router the options apply to
// the whole app, not to an individual Mount: there is one session cookie and
// one origin policy across every page mounted on it.
type Option func(*config)

func newConfig(opts []Option) *config {
	c := &config{
		trustedOrigins: map[string]bool{},
		maxSSEConn:     defaultMaxSSEConn,
		pinnedDeadline: defaultPinnedDeadline,
	}
	for _, opt := range opts {
		opt(c)
	}
	if c.maxSSEConn <= 0 {
		c.maxSSEConn = defaultMaxSSEConn
	}
	if c.pinnedDeadline <= 0 {
		c.pinnedDeadline = defaultPinnedDeadline
	}
	c.head.validate()
	if len(c.trustedOrigins) == 0 {
		originWarnOnce.Do(func() {
			log.Print("via: action endpoint accepts requests from any origin — the per-tab id is still " +
				"the CSRF token, but cross-origin enforcement is OFF until WithTrustedOrigin names one")
		})
	}
	return c
}

// originWarnOnce keeps the open-floor notice to one line per process. The floor
// is open by DEFAULT, so the quiet state is the permissive one, and an app that
// never calls WithTrustedOrigin looks configured rather than open — this line
// is what tells you which mode you are in.
//
// Deliberately untested: package-global and fires at most once per test binary,
// so whichever open-floor test runs first wins the line and every other sees
// nothing. An assertion would pass or fail on test order, not on the warning.
var originWarnOnce sync.Once

// WithTrustedOrigin turns on origin enforcement for the action endpoint and
// allowlists an exact origin (scheme://host[:port], as the browser sends it).
// With at least one set, only same-origin requests and listed origins are
// admitted; WITHOUT ANY, EVERY ORIGIN IS ACCEPTED — fine for development, set
// this in production.
func WithTrustedOrigin(origin string) Option {
	return func(c *config) { c.trustedOrigins[origin] = true }
}

// WithSessionTTL sets how long a session may sit idle before it expires
// (default 24h). Each access slides the window.
func WithSessionTTL(d time.Duration) Option {
	return func(c *config) {
		c.sessionTTL = d
	}
}

// WithSessionCookieName overrides the session cookie name (default
// "via_session"). Set a distinct name per app when two via apps share a host,
// or their session cookies clobber each other.
func WithSessionCookieName(name string) Option {
	return func(c *config) {
		c.sessionCookie = name
	}
}

// WithSecureCookies forces Secure on the session cookie even when via can't see
// TLS. By default Secure is set only when req.TLS != nil, which keeps plain
// http://localhost dev working — but behind a TLS-terminating proxy req.TLS is
// nil even though the user is on https, so set this there.
func WithSecureCookies() Option {
	return func(c *config) {
		c.sessionSecure = true
	}
}

// WithSessionStore points sessions at a shared, durable store instead of the
// default process-local map — the difference between a deploy logging every
// user out and a deploy nobody notices, and what makes a second pod see the
// first pod's sessions. Pair it with WithSessionKey: the key keeps the COOKIE
// valid, the store keeps the DATA behind it.
//
//	type redisSessions struct{ c *redis.Client }
//
//	func (r redisSessions) Load(ctx context.Context, id string) ([]byte, bool, error) {
//		b, err := r.c.Get(ctx, "via:"+id).Bytes()
//		if errors.Is(err, redis.Nil) {
//			return nil, false, nil
//		}
//		return b, err == nil, err
//	}
//
//	func (r redisSessions) Save(ctx context.Context, id string, data []byte, ttl time.Duration) error {
//		return r.c.Set(ctx, "via:"+id, data, ttl).Err()
//	}
//
//	func (r redisSessions) Delete(ctx context.Context, id string) error {
//		return r.c.Del(ctx, "via:"+id).Err()
//	}
//
//	via.NewRouter(via.WithSessionKey(key), via.WithSessionStore(redisSessions{c}))
func WithSessionStore(s SessionStore) Option {
	return func(c *config) {
		if s == nil {
			panic("via: WithSessionStore(nil)")
		}
		c.sessionStore = s
	}
}

// WithSessionStoreTimeout caps how long one session store round-trip may take
// (default 5s; a value of 0 or less restores the default). Without it a hung
// backend pins the request goroutine for as long as it hangs — session calls
// deliberately survive client cancellation, so the request's own context is no
// escape. A read-modify-write with retries (see [Session]) is bounded as a
// whole, not per attempt.
func WithSessionStoreTimeout(d time.Duration) Option {
	return func(c *config) {
		c.sessionTimeout = d
	}
}

// WithSessionKey sets the HMAC key signing the session cookie id. The key
// resolves WithSessionKey → VIA_SESSION_KEY → a random per-process key (warned
// on first use) — fine for dev, but those cookies survive neither a restart nor
// a second process, so set a stable key in production. It keeps the COOKIE
// valid only; the data behind it lives in the SessionStore, so a stable key
// without WithSessionStore still logs everyone out on restart.
func WithSessionKey(key []byte) Option {
	return func(c *config) {
		c.sessionKey = key
	}
}

// WithMaxSSEConn caps how many live SSE streams this Router serves at once
// (default 10000); past the cap a connect is refused 503. Each stream is a
// goroutine plus a composition tree held for the tab's life, so the right value
// is the one the box has memory for — raise it with the memory to back it, and
// lower it when a single pod should shed load to its siblings rather than swap.
// A value of 0 or less restores the default.
func WithMaxSSEConn(n int) Option {
	return func(c *config) { c.maxSSEConn = n }
}

// WithPinnedDeadline sets how long an action POST waits for the tab's stream
// goroutine to pick it up before answering 503 and logging the tab as pinned
// (default 5s). The goroutine is serialized across every Tick, Listen and
// action on that tab, so one handler that blocks stalls the rest; this deadline
// is what turns that into a diagnosable 503 instead of a hang. Set it below the
// load balancer's own timeout so via answers first. A value of 0 or less
// restores the default.
func WithPinnedDeadline(d time.Duration) Option {
	return func(c *config) { c.pinnedDeadline = d }
}
