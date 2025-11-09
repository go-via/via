package via

type LogLevel int

const (
	LogLevelError LogLevel = iota
	LogLevelWarn
	LogLevelInfo
	LogLevelDebug
)

type Plugin func(v *V)

// Config defines configuration options for the via application
type Options struct {
	// Level of the logs to write to stdout.
	// Options: Error, Warn, Info, Debug.
	LogLvl LogLevel

	// The title of the HTML document.
	DocumentTitle string

	// Plugins to extend the capabilities of the `Via` application.
	Plugins []Plugin
}
