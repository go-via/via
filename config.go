package via

import "time"

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

// WithPlugins registers plugins with the App.
func WithPlugins(plugins ...Plugin) Option {
	return func(c *config) { c.plugins = append(c.plugins, plugins...) }
}

// Plugin integrates with the Via app runtime. Implement Register to inject
// head elements, HTTP handlers, or other app-level concerns.
type Plugin interface {
	Register(*App)
}
