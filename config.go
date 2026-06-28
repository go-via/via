package via

import "time"

// config holds Register's optional settings. The zero set is the secure
// default: the action endpoint admits only same-origin requests. config is
// mutated only by Option values during Register; the option set is closed —
// users compose the provided WithX constructors, never author an option.
type config struct {
	trustedOrigins  map[string]bool
	insecureOrigin  bool
	theme           bool
	noReconnect     bool
	sseHeartbeat    time.Duration
	sseWriteTimeout time.Duration
	maxSSEConn      int
	sessions        bool
	sessionKey      []byte
	sessionTTL      time.Duration
	sessionCookie   string
	sessionSecure   bool
}

// defaultMaxSSEConn caps concurrent live SSE streams per Register so a client
// can't open island goroutines without bound. WithMaxSSEConnections overrides
// it; a non-positive override floors back to this default (the cap is never off).
const defaultMaxSSEConn = 10_000

// defaultWriteTimeout caps how long a single SSE frame write may block before
// the stream gives up on a stalled peer. WithSSEWriteTimeout overrides it; a
// non-positive override disables the deadline.
const defaultWriteTimeout = 10 * time.Second

// Option configures a Register call.
type Option func(*config)

func newConfig(opts []Option) *config {
	c := &config{trustedOrigins: map[string]bool{}, sseWriteTimeout: defaultWriteTimeout, maxSSEConn: defaultMaxSSEConn}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// WithTrustedOrigin allowlists an exact origin (scheme://host[:port], as the
// browser sends it in the Origin header) for the action endpoint, so a known
// cross-origin embedder is admitted even though the browser labels its requests
// cross-site.
func WithTrustedOrigin(origin string) Option {
	return func(c *config) { c.trustedOrigins[origin] = true }
}

// WithInsecureOrigin disables the action endpoint's origin floor entirely. Use
// it only for non-browser clients or local development — it removes the CSRF
// defense.
func WithInsecureOrigin() Option {
	return func(c *config) { c.insecureOrigin = true }
}

// WithTheme injects a small classless stylesheet into the page <head> so plain
// semantic markup (h1, ul, li, form, input, button) looks intentional — no
// class soup in the View. The stylesheet is nonce'd and admitted by the CSP.
// Omit it for an unstyled page.
func WithTheme() Option {
	return func(c *config) { c.theme = true }
}

// WithoutSSEReconnect drops via's built-in client reconnect manager from live
// pages. The manager surfaces a "Reconnecting…" banner on a dropped stream and
// reloads to re-bootstrap when Datastar gives up; opt out only when the app
// ships its own reconnect UX, to avoid double-serving it.
func WithoutSSEReconnect() Option {
	return func(c *config) { c.noReconnect = true }
}

// WithSSEHeartbeat sets the live stream's keepalive cadence. The keepalive is a
// comment frame whose only job is to keep the connection warm and to surface a
// silently-dropped (half-open) peer as a failed write, so the island goroutine
// and its timers don't leak. A non-positive d does NOT disable it — it floors to
// a safe default (25s). Keep it nonzero in production: a failed keepalive write
// is the only in-band detector of a peer that vanished without a FIN.
func WithSSEHeartbeat(d time.Duration) Option {
	return func(c *config) { c.sseHeartbeat = d }
}

// WithSSEWriteTimeout caps how long a single live-stream frame write may block
// before the stream tears down, so a stalled peer can't pin the island's single
// goroutine. Default 10s; a non-positive d disables the deadline.
func WithSSEWriteTimeout(d time.Duration) Option {
	return func(c *config) { c.sseWriteTimeout = d }
}

// WithMaxSSEConnections caps the number of concurrent live SSE streams a single
// Register will hold open; a connect past the cap is refused with 503. Default
// 10,000; a non-positive n floors to the default (the cap is never disabled).
func WithMaxSSEConnections(n int) Option {
	return func(c *config) { c.maxSSEConn = n }
}

// WithSessionTTL enables sessions and sets how long a session may sit idle
// before it expires (default 24h). Each access slides the window; an idle
// session past the TTL no longer resolves.
func WithSessionTTL(d time.Duration) Option {
	return func(c *config) {
		c.sessions = true
		c.sessionTTL = d
	}
}

// WithSessionCookieName enables sessions and overrides the session cookie name
// (default "via_session"). Set a distinct name per app when two via apps share a
// host, so their session cookies don't clobber each other.
func WithSessionCookieName(name string) Option {
	return func(c *config) {
		c.sessions = true
		c.sessionCookie = name
	}
}

// WithSecureCookies enables sessions and forces the Secure flag on the session
// cookie even when via can't see TLS on the request. By default Secure is set
// only when the request arrived over TLS (req.TLS != nil), which keeps plain
// http://localhost dev working; behind a TLS-terminating proxy req.TLS is nil
// even though the user is on https, so set this to keep the cookie https-only.
func WithSecureCookies() Option {
	return func(c *config) {
		c.sessions = true
		c.sessionSecure = true
	}
}

// WithSessionKey enables sessions and sets the HMAC key that signs the session
// cookie id. Without it (but with another session option) via generates a random
// per-process key and logs a warning — fine for dev, but cookies won't survive a
// restart or span processes, so set a stable key in production.
func WithSessionKey(key []byte) Option {
	return func(c *config) {
		c.sessions = true
		c.sessionKey = key
	}
}
