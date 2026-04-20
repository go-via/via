package via

import (
	"net/http/httptest"
	"time"
)

type LogLevel int

const (
	LogDebug LogLevel = iota
	LogInfo
	LogWarn
)

type config struct {
	addr            string
	title           string
	logLevel        LogLevel
	plugins         []Plugin
	shutdownTimeout  time.Duration
	sessionTTL      time.Duration
	contextTTL     time.Duration
	sseHeartbeat    time.Duration
	secureCookies   bool
	testServer      **httptest.Server
}

func (c *config) Addr() string         { return c.addr }
func (c *config) Title() string         { return c.title }
func (c *config) LogLevel() LogLevel   { return c.logLevel }
func (c *config) ShutdownTimeout() time.Duration { return c.shutdownTimeout }
func (c *config) SessionTTL() time.Duration { return c.sessionTTL }
func (c *config) SSEHeartbeat() time.Duration { return c.sseHeartbeat }
func (c *config) SecureCookies() bool   { return c.secureCookies }

type Option func(*config)

func WithAddr(addr string) Option {
	return func(c *config) { c.addr = addr }
}

func WithTitle(title string) Option {
	return func(c *config) { c.title = title }
}

func WithLogLevel(level LogLevel) Option {
	return func(c *config) { c.logLevel = level }
}

func WithShutdownTimeout(d time.Duration) Option {
	return func(c *config) { c.shutdownTimeout = d }
}

func WithSessionTTL(d time.Duration) Option {
	return func(c *config) { c.sessionTTL = d }
}

func WithSSEHeartbeat(d time.Duration) Option {
	return func(c *config) { c.sseHeartbeat = d }
}

func WithSecureCookies() Option {
	return func(c *config) { c.secureCookies = true }
}

func WithPlugins(plugins ...Plugin) Option {
	return func(c *config) { c.plugins = append(c.plugins, plugins...) }
}

func WithTestServer(server **httptest.Server) Option {
	return func(c *config) { c.testServer = server }
}

type Plugin interface {
	Register(*App)
}
