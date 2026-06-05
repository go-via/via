package via

import (
	"net/http"
	"net/http/httptest"
	"time"
)

// LogLevel selects the minimum log severity written to stdout.
type LogLevel int

const (
	LogDebug LogLevel = iota
	LogInfo
	LogWarn
	LogError
)

type config struct {
	addr               string
	title              string
	lang               string
	description        string
	logLevel           LogLevel
	plugins            []Plugin
	shutdownTimeout    time.Duration
	sessionTTL         time.Duration
	contextTTL         time.Duration
	reconcileInterval  time.Duration
	snapshotInterval   int
	foldVerify         bool
	keyStore           KeyStore
	sseHeartbeat       time.Duration
	sseWriteTimeout    time.Duration
	secureCookies      bool
	cookieSecuritySet  bool
	testServer         **httptest.Server
	httpServerHook     func(*http.Server)
	readHeaderTimeout  time.Duration
	readTimeout        time.Duration
	writeTimeout       time.Duration
	idleTimeout        time.Duration
	maxRequestBody     int64
	maxUploadSize      int64
	maxContexts        int
	actionErrorHandler func(*Ctx, error)
	logger             Logger
	notFoundHandler    http.Handler
	metrics            Metrics
	backplane          Backplane
}

// Option configures a via App.
type Option func(*config)

// WithAddr sets the HTTP listen address.
func WithAddr(addr string) Option { return func(c *config) { c.addr = addr } }

// WithTitle sets the rendered <title> on every page.
func WithTitle(title string) Option { return func(c *config) { c.title = title } }

// WithLang sets the <html lang="…"> attribute. Required for screen
// readers and language-aware browser features.
func WithLang(lang string) Option { return func(c *config) { c.lang = lang } }

// WithDescription sets the <meta name="description"> tag included in
// every rendered page. Search engines and link previews use it.
func WithDescription(d string) Option { return func(c *config) { c.description = d } }

// WithLogLevel sets the minimum log severity.
func WithLogLevel(level LogLevel) Option { return func(c *config) { c.logLevel = level } }

// WithShutdownTimeout sets the graceful shutdown timeout.
func WithShutdownTimeout(d time.Duration) Option { return func(c *config) { c.shutdownTimeout = d } }

// WithSessionTTL sets the per-session expiry. Default 30 minutes.
func WithSessionTTL(d time.Duration) Option { return func(c *config) { c.sessionTTL = d } }

// WithContextTTL sets how long a *stream-less* tab Ctx lingers before the
// idle sweep reclaims it. Default 15 minutes; a value <= 0 disables the
// sweep (contexts never expire).
//
// It governs only ctxs with no open SSE stream — a page GET that never
// opened the stream, or the gap after a stream drops. A connected tab is
// kept alive for its stream's lifetime regardless of this value, so a short
// TTL can never reap a live tab.
func WithContextTTL(d time.Duration) Option { return func(c *config) { c.contextTTL = d } }

// WithReconcileInterval sets how often each pod re-pulls its value-shaped
// StateApp keys to the backplane Store HEAD. This periodic sweep makes the
// changes feed a pure latency optimization: a pod converges to shared state
// even when no Change hint reached it (a pod that joined after the write, a
// crash between the CAS and the hint append, or a silent Update). 0 disables
// the sweep (the changes feed alone then carries convergence). Default 5s.
func WithReconcileInterval(d time.Duration) Option {
	return func(c *config) { c.reconcileInterval = d }
}

// WithSnapshotInterval sets how many folds a StateAppEvents projector applies
// before persisting a fold snapshot, so a cold start replays only the tail
// after the snapshot's offset instead of re-folding the whole log. 0 (or less)
// disables snapshot writes. The snapshot is a disposable cache — never required
// for correctness; a missing or stale-codec snapshot just re-folds from genesis.
// Default 64.
func WithSnapshotInterval(n int) Option { return func(c *config) { c.snapshotInterval = n } }

// WithFoldVerify turns on runtime fold-determinism checking for every
// StateAppEvents projector: each record is folded a SECOND time from the same
// accumulator and the two results compared. A mismatch means the reducer is
// impure (it read a clock, RNG, mutable global, or otherwise depends on
// something other than (acc, event)) — the runtime emits via.fold.divergence AND
// permanently REFUSES to compact that key, so a non-deterministic fold can never
// be crystallized into a durable-genesis snapshot (which no later re-fold could
// recover). It roughly doubles fold CPU, so it is opt-in — run it in dev/CI (and
// optionally a canary pod) to catch impurity before it reaches production
// compaction. Off by default.
//
// The check uses reflect.DeepEqual on the two fold results, which can spuriously
// flag a PURE fold whose V contains uncomparable parts (func/chan fields, or NaN
// floats — for which a==a is false). That is fail-SAFE, not fail-open: the worst
// case is a needless via.fold.divergence and a refusal to compact, never a bad
// snapshot. Such a V is also not JSON-snapshottable, so it could not be compacted
// anyway.
func WithFoldVerify() Option { return func(c *config) { c.foldVerify = true } }

// WithKeyStore enables per-data-subject encryption (crypto-shred GDPR erasure)
// for StateAppEvents. Events whose type implements DataSubject have their
// payload encrypted under the subject's key (from the KeyStore) before they are
// appended to the durable log, and App.EraseDataSubject drops a subject's key so
// every ciphertext for them becomes permanently unreadable — even in an
// append-only log or a backup — without rewriting history. Non-DataSubject
// events are unaffected (stored plaintext).
//
// In a cluster every pod must share the SAME KeyStore (a KMS/Vault-backed impl),
// or a pod without a subject's key cannot decode that subject's events.
func WithKeyStore(ks KeyStore) Option { return func(c *config) { c.keyStore = ks } }

// WithSSEHeartbeat sets the SSE keepalive cadence. Default 25s.
//
// A connected tab is kept alive for its stream's lifetime regardless of this
// value — the keepalive's only job is to detect a silently-dropped (half-
// open) client via a failed write, which then reaps the Ctx. Because that
// failed write is the sole in-band detector of a vanished client, a value
// <= 0 does NOT disable the keepalive; it floors to a safe default (25s).
// Slow it down if you must, but it can't be silenced.
func WithSSEHeartbeat(d time.Duration) Option { return func(c *config) { c.sseHeartbeat = d } }

// WithSSEWriteTimeout caps how long a single SSE drain may block on the
// underlying connection before the stream is torn down. Bounds the
// blast radius of slow / stalled clients (without this, a wedged TCP
// peer pins the server goroutine for the lifetime of the tab). Default
// 10 seconds.
//
// Keep it nonzero in production: a failed keepalive write is the only
// in-band detector of a vanished (half-open) client, and a connected Ctx
// is never TTL-swept — so setting 0 lets a half-open peer pin its Ctx and
// goroutine until the OS TCP keepalive fires (or the process exits).
func WithSSEWriteTimeout(d time.Duration) Option {
	return func(c *config) { c.sseWriteTimeout = d }
}

// WithSecureCookies marks the session cookie Secure. This is the default;
// the option remains for explicit intent and conflicts with
// [WithInsecureCookies].
func WithSecureCookies() Option {
	return func(c *config) {
		if c.cookieSecuritySet && !c.secureCookies {
			panic("via: conflicting cookie security options")
		}
		c.secureCookies = true
		c.cookieSecuritySet = true
	}
}

// WithInsecureCookies clears the Secure flag so the session cookie rides
// a plain-http origin. The Secure default is the safe production posture
// (a framework aimed at internal tools should not ship a cookie that leaks
// on an http downgrade); reach for this only on a local http:// dev loop.
// Conflicts with [WithSecureCookies].
func WithInsecureCookies() Option {
	return func(c *config) {
		if c.cookieSecuritySet && c.secureCookies {
			panic("via: conflicting cookie security options")
		}
		c.secureCookies = false
		c.cookieSecuritySet = true
	}
}

// WithPlugins registers plugins. They run Register at New time.
func WithPlugins(plugins ...Plugin) Option {
	return func(c *config) { c.plugins = append(c.plugins, plugins...) }
}

// WithTestServer creates an httptest.Server bound to the app's handler and
// writes it to *server before New returns. Caller must Close it.
func WithTestServer(server **httptest.Server) Option {
	return func(c *config) { c.testServer = server }
}

// WithHTTPServer hands the user the *http.Server before listening so
// non-default fields (TLSConfig, ConnState, …) can be set.
func WithHTTPServer(hook func(*http.Server)) Option {
	return func(c *config) { c.httpServerHook = hook }
}

// WithReadHeaderTimeout overrides the default 10 s read-header timeout.
func WithReadHeaderTimeout(d time.Duration) Option {
	return func(c *config) { c.readHeaderTimeout = d }
}

// WithReadTimeout sets http.Server.ReadTimeout. The SSE handler doesn't
// honor it (the stream is meant to be long-lived), but action POSTs do.
func WithReadTimeout(d time.Duration) Option { return func(c *config) { c.readTimeout = d } }

// WithWriteTimeout sets http.Server.WriteTimeout. Be cautious: SSE
// streams are long-lived, so a non-zero WriteTimeout can cut them off
// mid-stream. Default 0 (no timeout) is safer for SSE-heavy apps.
func WithWriteTimeout(d time.Duration) Option { return func(c *config) { c.writeTimeout = d } }

// WithIdleTimeout overrides the default 120 s idle-timeout. Affects the
// lifetime of HTTP/1.1 keep-alive connections; SSE streams are exempt.
func WithIdleTimeout(d time.Duration) Option { return func(c *config) { c.idleTimeout = d } }

// WithMaxRequestBody caps body bytes for action POSTs that ship as
// application/json (Datastar's default action payload). Default 1 MiB.
// File-upload actions use a multipart body and are governed by the
// separate [WithMaxUploadSize] knob, since file parts inflate body
// size beyond what a typed-signal JSON payload ever needs.
func WithMaxRequestBody(n int64) Option { return func(c *config) { c.maxRequestBody = n } }

// WithMaxUploadSize caps total multipart body bytes for action POSTs
// that include file parts. Default 32 MiB. The cap applies to the wire
// body (all files + form fields combined); the per-file in-memory cap
// is the lower of this value and 32 MiB before parts spill to disk.
func WithMaxUploadSize(n int64) Option { return func(c *config) { c.maxUploadSize = n } }

// WithMaxContexts caps the number of concurrent live tabs. New page
// renders past the cap return 503 instead of registering a Ctx — a
// crude but effective floor against tab-spam DoS. Default 0 (no
// cap). Tune to (expected peak users × tabs per user × 2).
func WithMaxContexts(n int) Option { return func(c *config) { c.maxContexts = n } }

// WithActionErrorHandler replaces the default browser-alert with a custom
// callback for action errors and panics. The error from a panic is wrapped
// as fmt.Errorf("panic: %v", recovered).
func WithActionErrorHandler(fn func(*Ctx, error)) Option {
	return func(c *config) { c.actionErrorHandler = fn }
}

// WithLogger replaces the default log.Printf-backed logger with a custom
// Logger (slog, zap, zerolog, a test buffer, …). All runtime warnings
// and errors flow through this callback as level + message + key/value
// pairs.
func WithLogger(l Logger) Option { return func(c *config) { c.logger = l } }

// WithNotFound replaces the default 404 page with a custom handler. The
// handler runs after the session middleware, so it can read the session
// and decide whether to redirect, render a "not found" composition, or
// short-circuit with an empty body.
func WithNotFound(h http.Handler) Option { return func(c *config) { c.notFoundHandler = h } }

// WithMetrics installs a [Metrics] backend that receives counter / gauge
// / histogram events for actions, renders, SSE connect/disconnect, and
// tab-count gauges. Default is a no-op backend, so configuring this is
// purely additive. See the [Metrics] godoc for the event catalogue.
func WithMetrics(m Metrics) Option { return func(c *config) { c.metrics = m } }

// WithBackplane wires the state backplane that makes app/session-scoped
// reactive state survive restarts and span a cluster. The default (no option,
// or a nil b) resolves internally to [InMemory], so the Backplane interface is
// exercised on every single-pod run and there is no nil-special-case path. Wire
// it once at boot; it is never swapped at runtime.
func WithBackplane(b Backplane) Option { return func(c *config) { c.backplane = b } }

// Plugin extends the App at registration time.
type Plugin interface {
	Register(*App)
}
