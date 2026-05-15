package h

import "fmt"

// expr formats only when there are args — saves a Sprintf round-trip
// on plain literals and lets callers pass strings that contain a bare
// '%' (otherwise fmt would emit %!(NOVERB)).
func expr(format string, args []any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// DataInit runs an expression once when the page loads.
// Use for one-time setup or fetching initial data.
// Example: DataInit("@get('/api/data')")
func DataInit(format string, args ...any) H {
	return Data("init", expr(format, args))
}

// DataIgnoreMorph tells Datastar to skip morphing this element during
// updates. Use when you want to manually control an element's DOM.
func DataIgnoreMorph() H {
	return Attr("data-ignore-morph")
}

// DataShow conditionally shows/hides an element based on a boolean
// expression. Example: DataShow("$count > 0")
func DataShow(format string, args ...any) H {
	return Data("show", expr(format, args))
}

// DataOnClick creates a click event handler for Datastar. Use for
// frontend-only signals; for server actions use on.Click.
// Example: DataOnClick("$count = $count + 1")
func DataOnClick(format string, args ...any) H {
	return Data("on:click", expr(format, args))
}

// DataClass conditionally adds/removes CSS classes based on a boolean
// expression. Example: DataClass("active", "$isActive")
func DataClass(className, format string, args ...any) H {
	return Data("class:"+className, expr(format, args))
}
