package via

import "github.com/go-via/via/h"

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

	// Elements to include in the head of the base HTML document.
	// Useful for including css stylesheets and JS scripts.
	DocumentHeadIncludes []h.H

	// Elements to include in the bottom of the body of the base
	// HTML document.Useful for including JS scripts or a footer.
	DocumentBodyIncludes []h.H

	// Plugins to extend the capabilities of the `Via` application.
	// Check `https://github.com/go-via/plugins` for a list of available plugins.
	Plugins []Plugin
}
