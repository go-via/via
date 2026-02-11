package via

type LogLevel int

const (
	undefined LogLevel = iota
	LogLevelError
	LogLevelWarn
	LogLevelInfo
	LogLevelDebug
)

// Plugin is a func that can mutate the given *via.V app runtime. It is useful to integrate popular JS/CSS UI libraries or tools.
type Plugin func(v *V)

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

	// Plugins to extend the capabilities of the `Via` application.
	Plugins []Plugin
}
