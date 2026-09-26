package via

import (
	"fmt"
	"log/slog"
	"net/url"
	"strings"
	"time"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// config holds Handler's optional settings. The zero set is the dev-friendly
// default: the action endpoint accepts requests from any origin, and production
// opts into enforcement with WithTrustedOrigin.
type config struct {
	log            *slog.Logger
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
	maxBody        int64
	maxUpload      int64
	unsafeEval     bool
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

type mountConfig struct{}

// MountOption configures a single Mount call, as opposed to Option which
// configures the whole Router.
type MountOption func(*mountConfig)

func newConfig(opts []Option) *config {
	c := &config{
		trustedOrigins: map[string]bool{},
		maxSSEConn:     defaultMaxSSEConn,
		pinnedDeadline: defaultPinnedDeadline,
		maxBody:        maxActionBody,
		maxUpload:      maxUploadBytes,
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
	if c.log == nil {
		c.log = slog.Default()
	}
	c.head.validate()
	if len(c.trustedOrigins) == 0 {
		c.log.Warn(originNotice)
	}
	if c.unsafeEval {
		c.log.Warn(unsafeEvalNotice)
	}
	return c
}

// originNotice is logged once per Router when no trusted origin is set. The
// floor is open by default, so the quiet state is the permissive one, and an
// app that never calls WithTrustedOrigin looks configured rather than open —
// this line is what tells you which mode you are in.
const originNotice = "via: no WithTrustedOrigin set, so actions accept requests from any origin: any site can fire an action that needs no session, and a page on a sibling subdomain, or this host over http, can fire one as the signed-in user. Set WithTrustedOrigin in production"

// unsafeEvalNotice is logged once per Router while WithUnsafeEval is set: the
// option is one line in a list of options, and this is what keeps the loss of
// the CSP's eval guard from reading as configuration.
const unsafeEvalNotice = "via: WithUnsafeEval set, so every page's CSP allows 'unsafe-eval': a string an attacker gets into eval, Function or setTimeout(string) now runs as script. Set it only for a library that needs it"

// WithLogger routes via's own diagnostics to l. Default is slog.Default(). It
// panics on nil.
//
// The h package and via/topic have no Router to reach and keep writing to
// slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(c *config) {
		if l == nil {
			panic("via: WithLogger(nil)")
		}
		c.log = l
	}
}

// WithTrustedOrigin turns on origin enforcement for the action endpoint and
// the SSE connect, and allowlists an origin (scheme://host[:port]). With at
// least one set, only same-origin requests and listed origins are admitted,
// and a request that carries no origin signal is refused. WITHOUT ANY, EVERY
// ORIGIN IS ACCEPTED, including requests that carry no origin signal, and the
// Router logs a warning at startup — fine for development, set this in
// production. Ctx.Redirect also follows absolute URLs on a listed origin.
//
// The origin is compared the way the browser serializes it: the host is
// lower-cased and a default port dropped, so "https://Auth.example:443"
// matches an Origin of "https://auth.example", and a trailing "/" is
// dropped. It panics on a value that is not a bare http(s) origin: one with a
// path, a query, a fragment or userinfo.
func WithTrustedOrigin(origin string) Option {
	return func(c *config) { c.trustedOrigins[bareOrigin(origin)] = true }
}

// bareOrigin normalizes a WithTrustedOrigin value the way the browser
// serializes an Origin header, and panics on anything that is more than an
// origin: the browser never sends a path or query, so such an entry would
// silently match nothing.
func bareOrigin(origin string) string {
	bad := func(part string) {
		panic(fmt.Sprintf("via: WithTrustedOrigin(%q): %s; want a bare origin, scheme://host[:port]", origin, part))
	}
	u, err := url.Parse(origin)
	switch {
	case err != nil:
		bad("does not parse: " + err.Error())
	case u.Scheme != "http" && u.Scheme != "https":
		bad("scheme is not http or https")
	case u.User != nil:
		bad("has userinfo")
	case u.Host == "":
		bad("has no host")
	case u.Path != "" && u.Path != "/" || u.RawPath != "":
		bad("has a path")
	case u.RawQuery != "" || u.ForceQuery:
		bad("has a query")
	case u.Fragment != "" || strings.Contains(origin, "#"):
		bad("has a fragment")
	}
	norm, ok := hcore.URLOrigin(origin)
	if !ok || norm == "" {
		bad("is not an origin")
	}
	return norm
}

// WithSessionTTL sets how long a session may sit idle before it expires
// (default 24h). Each access slides the window; once less than half of it is
// left, the access re-saves the session and re-sends the cookie so the
// browser's expiry moves with it.
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

// WithSecureCookies forces Secure on the session cookie even when via can't
// tell the browser is on https. By default Secure is set when the request came
// over TLS or a proxy says https in X-Forwarded-Proto or Forwarded (proto=),
// which keeps plain http://localhost dev working. Set this behind a
// TLS-terminating proxy that sends neither header.
func WithSecureCookies() Option {
	return func(c *config) {
		c.sessionSecure = true
	}
}

// WithSessionStore points sessions at a shared, durable store instead of the
// default process-local map — the difference between a deploy logging every
// user out and a deploy nobody notices, and what makes a second pod see the
// first pod's sessions. Pair it with WithSessionKey: the key keeps the cookie
// valid, the store keeps the data behind it.
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
// a second process, so set a stable key in production. It keeps the cookie
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
// The same number, up to 1024, caps the actions parked router-wide waiting
// for a tab's stream to connect; past it an action answers 503 at once. A
// value of 0 or less restores the default.
func WithMaxSSEConn(n int) Option {
	return func(c *config) { c.maxSSEConn = n }
}

// WithPinnedDeadline sets how long an action POST waits for the tab's stream
// goroutine to pick it up before answering 503 and logging the tab as pinned
// (default 5s). The goroutine is serialized across every Tick, Listen and
// action on that tab, so one handler that blocks stalls the rest; this deadline
// is what turns that into a diagnosable 503 instead of a hang. It bounds only
// the wait to be picked up: an action that has started running is waited for,
// because it writes the POST's own response.
//
// It also bounds how long an action carrying a tab id this process issued
// waits for that tab's stream to connect; one deadline covers both waits. A
// click made before the stream opens, or during a reconnect, runs once it
// does, in arrival order. It answers 410 if the stream has not come by 2s or
// d, whichever is sooner, and 503 if it came but did not pick the action up
// by d.
// When via itself ended the stream (a Rotate, a revoked session, an aborted
// push, Router.Close), the id answers 410 at once for d instead of waiting.
// Set it below the load balancer's own timeout so via answers first. A value
// of 0 or less restores the default.
//
// The tab is logged as pinned too when any one handler, or one frame write to
// a client that stopped reading, runs past d with no action waiting, so a tab
// nobody clicks on is still reported. The line says which of the two it is.
func WithPinnedDeadline(d time.Duration) Option {
	return func(c *config) { c.pinnedDeadline = d }
}

// WithMaxBody caps an action POST body in bytes, and how much of a native form
// submit stays in RAM before the rest spills to a temp file (default 1 MiB).
// Over the cap the request answers 413. It panics on a value of 0 or less.
func WithMaxBody(bytes int64) Option {
	return func(c *config) {
		if bytes <= 0 {
			panic("via: WithMaxBody must be positive")
		}
		c.maxBody = bytes
	}
}

// WithMaxUpload caps a native PostForm submit's whole multipart body in bytes
// (default 8 MiB). Over the cap the request answers 413. It panics on a value
// of 0 or less.
func WithMaxUpload(bytes int64) Option {
	return func(c *config) {
		if bytes <= 0 {
			panic("via: WithMaxUpload must be positive")
		}
		c.maxUpload = bytes
	}
}

// WithUnsafeEval adds 'unsafe-eval' to every mount's script-src, for a
// library that compiles code from strings (eval, Function, setTimeout with a
// string). The nonce and hashes stay. Without it the policy forbids eval,
// which Datastar does not need. While it is set, the Router logs a warning at
// startup.
func WithUnsafeEval() Option {
	return func(c *config) { c.unsafeEval = true }
}
