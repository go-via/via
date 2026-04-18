package via

import (
	"net/http/httptest"
	"time"
)

// LogLevel controls the minimum severity written to stdout.
type LogLevel int

const (
	LogDebug LogLevel = iota
	LogInfo
	LogWarn
	LogError
)

type config struct {
	addr            string
	title           string
	logLevel        LogLevel
	plugins         []Plugin
	shutdownTimeout time.Duration
	sessionTTL      time.Duration
	contextTTL      time.Duration
	secureCookies   bool
	testServer      **httptest.Server
}

// Option configures a Via App.
type Option func(*config)

// WithAddr sets the HTTP server listen address (e.g. ":3000").
func WithAddr(addr string) Option {
	return func(c *config) { c.addr = addr }
}

// WithTitle sets the HTML document title.
func WithTitle(title string) Option {
	return func(c *config) { c.title = title }
}

// WithLogLevel sets the minimum log level to write to stdout.
func WithLogLevel(level LogLevel) Option {
	return func(c *config) { c.logLevel = level }
}

// WithShutdownTimeout sets the graceful shutdown timeout for draining connections.
// Defaults to 5 seconds.
func WithShutdownTimeout(d time.Duration) Option {
	return func(c *config) { c.shutdownTimeout = d }
}

// WithSessionTTL sets the session expiry duration. Defaults to 30 minutes.
func WithSessionTTL(d time.Duration) Option {
	return func(c *config) { c.sessionTTL = d }
}

// WithContextTTL sets the idle timeout for per-tab contexts (Ctx). A context
// is swept when it hasn't been touched by an SSE event, action, or registration
// for longer than d. Use 0 to disable the sweep. Defaults to 15 minutes.
func WithContextTTL(d time.Duration) Option {
	return func(c *config) { c.contextTTL = d }
}

// WithSecureCookies marks the session cookie as Secure so browsers only send
// it over HTTPS. Enable this in production behind TLS.
func WithSecureCookies() Option {
	return func(c *config) { c.secureCookies = true }
}

// WithPlugins registers plugins with the App.
func WithPlugins(plugins ...Plugin) Option {
	return func(c *config) { c.plugins = append(c.plugins, plugins...) }
}

// WithTestServer creates an httptest.Server backed by the app's mux and writes
// it to *server before New returns. The caller must call (*server).Close.
func WithTestServer(server **httptest.Server) Option {
	return func(c *config) { c.testServer = server }
}

// Plugin integrates with the Via app runtime. Implement Register to inject
// head elements, HTTP handlers, or other app-level concerns.
type Plugin interface {
	Register(*App)
}
