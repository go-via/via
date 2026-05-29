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

// WithContextTTL sets the per-tab Ctx idle expiry. Default 15 minutes.
func WithContextTTL(d time.Duration) Option { return func(c *config) { c.contextTTL = d } }

// WithSSEHeartbeat sets the SSE keep-alive interval.
func WithSSEHeartbeat(d time.Duration) Option { return func(c *config) { c.sseHeartbeat = d } }

// WithSSEWriteTimeout caps how long a single SSE drain may block on the
// underlying connection before the stream is torn down. Bounds the
// blast radius of slow / stalled clients (without this, a wedged TCP
// peer pins the server goroutine for the lifetime of the tab). Default
// 10 seconds; set 0 to disable the deadline.
func WithSSEWriteTimeout(d time.Duration) Option {
	return func(c *config) { c.sseWriteTimeout = d }
}

// WithSecureCookies marks the session cookie Secure. This is the default;
// the option remains for explicit intent and conflicts with
// [WithInsecureCookies].
func WithSecureCookies() Option {
	return func(c *config) {
		if c.cookieSecuritySet {
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
		if c.cookieSecuritySet {
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

// Plugin extends the App at registration time.
type Plugin interface {
	Register(*App)
}
