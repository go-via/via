package h

import "fmt"

// expr formats only when args are present, so callers can pass literal
// expressions containing a bare '%' without the fmt package mangling
// them with NOVERB markers.
func expr(format string, args []any) string {
	if len(args) == 0 {
		return format
	}
	return fmt.Sprintf(format, args...)
}

// DataInit runs an expression once when the page loads.
func DataInit(format string, args ...any) H {
	return Data("init", expr(format, args))
}

// DataIgnoreMorph tells datastar to skip morphing this element on
// patch.
func DataIgnoreMorph() H {
	return buildBool("data-ignore-morph")
}

// DataShow conditionally shows/hides the element based on the
// expression's truthiness.
func DataShow(format string, args ...any) H {
	return Data("show", expr(format, args))
}

// DataOnClick attaches a datastar click handler. Use for frontend-only
// signal mutations; for server actions prefer the `on` package.
func DataOnClick(format string, args ...any) H {
	return Data("on:click", expr(format, args))
}

// DataClass conditionally adds/removes a CSS class.
func DataClass(className, format string, args ...any) H {
	return Data("class:"+className, expr(format, args))
}
