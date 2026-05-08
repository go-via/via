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

// DataEffect runs an expression continuously when reactive data changes.
// Use for reacting to signal changes.
// Example: DataEffect("console.log($count)")
func DataEffect(format string, args ...any) H {
	return Data("effect", expr(format, args))
}

// DataIgnoreMorph tells Datastar to skip morphing this element during updates.
// Use when you want to manually control an element's DOM.
func DataIgnoreMorph() H {
	return Attr("data-ignore-morph")
}

// DataShow conditionally shows/hides an element based on a boolean expression.
// Example: DataShow("$count > 0")
func DataShow(format string, args ...any) H {
	return Data("show", expr(format, args))
}

// DataText sets the text content of an element from a signal.
// Only useful for frontend-only signals (prefixed with _).
// For normal signals, use via.Signal().Text() instead.
// Example: DataText("_name")
func DataText(format string, args ...any) H {
	return Data("text", expr(format, args))
}

// DataOnClick creates a click event handler for Datastar.
// Example: DataOnClick("$count = $count + 1")
func DataOnClick(format string, args ...any) H {
	return Data("on:click", expr(format, args))
}

// DataOnChange creates a change event handler for Datastar.
// Example: DataOnChange("$value = $event.target.value")
func DataOnChange(format string, args ...any) H {
	return Data("on:change", expr(format, args))
}

// DataBind creates a two-way binding for form elements with frontend-only signals (prefixed with _).
// For normal server-side signals, use via.Signal().Bind() instead.
// Example: DataBind("_theme")
func DataBind(format string, args ...any) H {
	return Data("bind", expr(format, args))
}

// DataSignals creates signal initializations for frontend-only signals (prefixed with _).
// These signals exist only in the browser and are not synced with the server.
// For normal server-side signals, use via.Signal() instead.
// Example: DataSignals(`{"_theme": "blue", "_count": 0}`)
func DataSignals(format string, args ...any) H {
	return Data("signals", expr(format, args))
}

// DataAttr dynamically sets any HTML attribute based on a reactive expression.
// Example: DataAttr("href", "$url")
func DataAttr(name, format string, args ...any) H {
	return Data("attr:"+name, expr(format, args))
}

// DataClass conditionally adds/removes CSS classes based on a boolean expression.
// Example: DataClass("active", "$isActive")
func DataClass(className, format string, args ...any) H {
	return Data("class:"+className, expr(format, args))
}

// DataStyle dynamically sets inline styles based on a reactive expression.
// Example: DataStyle("color", "$textColor")
func DataStyle(property, format string, args ...any) H {
	return Data("style:"+property, expr(format, args))
}

// DataOnInput handles input events for text input elements.
// Example: DataOnInput("$value = $event.target.value")
func DataOnInput(format string, args ...any) H {
	return Data("on:input", expr(format, args))
}

// DataOnSubmit handles form submit events.
// Example: DataOnSubmit("@post('/api/submit')")
func DataOnSubmit(format string, args ...any) H {
	return Data("on:submit", expr(format, args))
}

// DataOnKeyDown handles keydown events.
// Example: DataOnKeyDown("$key = $event.key")
func DataOnKeyDown(format string, args ...any) H {
	return Data("on:keydown", expr(format, args))
}

// DataOnKeyUp handles keyup events.
// Example: DataOnKeyUp("$key = $event.key")
func DataOnKeyUp(format string, args ...any) H {
	return Data("on:keyup", expr(format, args))
}

// DataOnFocus handles focus events.
// Example: DataOnFocus("$focused = true")
func DataOnFocus(format string, args ...any) H {
	return Data("on:focus", expr(format, args))
}

// DataOnBlur handles blur events (when element loses focus).
// Example: DataOnBlur("$focused = false")
func DataOnBlur(format string, args ...any) H {
	return Data("on:blur", expr(format, args))
}

// DataOnMouseOver handles mouseover events (bubbles — fires for every
// descendant entry). Use DataOnMouseEnter via the on/* package if you
// want non-bubbling enter semantics.
// Example: DataOnMouseOver("$hover = true")
func DataOnMouseOver(format string, args ...any) H {
	return Data("on:mouseover", expr(format, args))
}

// DataOnMouseOut handles mouseout events (bubbles — fires for every
// descendant exit). Use DataOnMouseLeave via the on/* package if you
// want non-bubbling leave semantics.
// Example: DataOnMouseOut("$hover = false")
func DataOnMouseOut(format string, args ...any) H {
	return Data("on:mouseout", expr(format, args))
}
