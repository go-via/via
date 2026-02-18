package via

type LogLevel int

const (
	undefined LogLevel = iota
	LogLevelError
	LogLevelWarn
	LogLevelInfo
	LogLvlDEBUG
)

// Plugin is an interface that Via plugins must implement.
// Plugins register themselves with the Via app via the Register method.
type Plugin interface {
	Register(v *V)
}

// Options defines configuration options for the via application
type Options struct {
	// The development mode flag. If true, enables server and browser auto-reload on `.go` file changes.
	DevMode bool
	// The http server address. e.g. ':3000'
	ServerAddress string

	// Level of the logs to write to stdout.
	// Options: Error, Warn, Info, Debug.
	LogLvl LogLevel

	// The title of the HTML document.
	DocumentTitle string

	// SessionTTL is the duration after which inactive sessions are cleaned up.
	// Default is 30 minutes. Set to 0 to disable automatic cleanup.
	SessionTTL int

	// SessionCookieName is the name of the session cookie.
	// Default is "via_sid".
	SessionCookieName string

	// SessionCookieMaxAge is the max age of the session cookie in seconds.
	// Default is 30 days (2592000 seconds).
	SessionCookieMaxAge int

	// Plugins to extend the capabilities of the `Via` application.
	Plugins []Plugin
}
