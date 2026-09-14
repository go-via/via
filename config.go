package via

import (
	"log"
	"sync"
	"time"
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
	csp            string
}

// sseHeartbeat is the keepalive cadence. Fixed, never configurable: a failed
// keepalive write is the only in-band detector of a peer that vanished without
// a FIN, so the stream goroutine and its timers would leak without it.
const sseHeartbeat = 25 * time.Second

// sseWriteTimeout caps a single frame write, so a stalled peer can't pin the
// embed's goroutine. Fixed: disabling it would let one pin a net/http goroutine
// per click, since an action POST waits behind this same deadline.
const sseWriteTimeout = 10 * time.Second

// maxSSEConn caps concurrent live SSE streams per Handler; past it a connect
// is refused 503. var rather than const only so a test can shrink it.
var maxSSEConn = 10_000

// Option configures a Handler or a NewRouter. On a Router the options apply to
// the whole app, not to an individual Mount: there is one session cookie and
// one origin policy across every page mounted on it.
type Option func(*config)

func newConfig(opts []Option) *config {
	c := &config{trustedOrigins: map[string]bool{}}
	for _, opt := range opts {
		opt(c)
	}
	c.head.validate()
	c.csp = buildCSP(c.head)
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
