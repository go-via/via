package via

import (
	"log"
	"sync"
	"time"
)

// config holds Register's optional settings. The zero set is the dev-friendly
// default: the action endpoint accepts requests from any origin. Production
// deployments opt into origin enforcement with WithTrustedOrigin. config is
// mutated only by Option values during Register; the option set is closed —
// users compose the provided WithX constructors, never author an option.
type config struct {
	trustedOrigins map[string]bool
	sessionKey     []byte
	sessionTTL     time.Duration
	sessionCookie  string
	sessionSecure  bool
	head           Head
	csp            string
}

// sseHeartbeat is the live stream's keepalive cadence: a comment frame whose
// only job is to keep the connection warm and surface a silently-dropped
// (half-open) peer as a failed write, so the island goroutine and its timers
// don't leak. Fixed — a failed keepalive write is the only in-band detector of
// a peer that vanished without a FIN, so it is never disabled.
const sseHeartbeat = 25 * time.Second

// sseWriteTimeout caps how long a single live-stream frame write may block
// before the stream tears down, so a stalled peer can't pin the island's
// single goroutine. Fixed — disabling it would let a stalled peer pin a
// net/http goroutine per click (an action POST against a stalled stream waits
// behind this same deadline, see liveConn.run).
const sseWriteTimeout = 10 * time.Second

// maxSSEConn caps the number of concurrent live SSE streams a single Register
// will hold open; a connect past the cap is refused with 503. var rather than
// const only so a test can shrink it — there is no way to change it in
// production.
var maxSSEConn = 10_000

// Option configures a Register call.
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
// is open by DEFAULT, so the quiet state is the permissive one: WithTrustedOrigin
// reads as "allow this origin" and says nothing about also switching enforcement
// on, which means an app that never calls it looks configured rather than open.
// This is the line that tells you which mode you are actually in.
var originWarnOnce sync.Once

// WithTrustedOrigin turns on origin enforcement for the action endpoint and
// allowlists an exact origin (scheme://host[:port], as the browser sends it in
// the Origin header). With at least one trusted origin set, only same-origin
// requests and listed origins are admitted; without any, the endpoint accepts
// every origin — fine for development, set this in production.
func WithTrustedOrigin(origin string) Option {
	return func(c *config) { c.trustedOrigins[origin] = true }
}

// WithSessionTTL sets how long a session may sit idle
// before it expires (default 24h). Each access slides the window; an idle
// session past the TTL no longer resolves.
func WithSessionTTL(d time.Duration) Option {
	return func(c *config) {
		c.sessionTTL = d
	}
}

// WithSessionCookieName overrides the session cookie name
// (default "via_session"). Set a distinct name per app when two via apps share a
// host, so their session cookies don't clobber each other.
func WithSessionCookieName(name string) Option {
	return func(c *config) {
		c.sessionCookie = name
	}
}

// WithSecureCookies forces the Secure flag on the session cookie even when via can't see TLS on the request. By default Secure is set
// only when the request arrived over TLS (req.TLS != nil), which keeps plain
// http://localhost dev working; behind a TLS-terminating proxy req.TLS is nil
// even though the user is on https, so set this to keep the cookie https-only.
func WithSecureCookies() Option {
	return func(c *config) {
		c.sessionSecure = true
	}
}

// WithSessionKey sets the HMAC key that signs the session cookie id. Sessions
// are always available; the key resolves WithSessionKey → VIA_SESSION_KEY env →
// a random per-process key (warned on first use) — fine for dev, but cookies
// won't survive a restart or span processes, so set a stable key in production.
func WithSessionKey(key []byte) Option {
	return func(c *config) {
		c.sessionKey = key
	}
}
