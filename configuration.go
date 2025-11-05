package via

import "github.com/go-via/via/h"

type LogLevel int

const (
	LogLevelError LogLevel = iota
	LogLevelWarn
	LogLevelInfo
	LogLevelDebug
)

// Config defines configuration options for the via application
type Configuration struct {
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
}
