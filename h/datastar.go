package h

import "fmt"

// DataInit runs an expression once when the page loads.
// Use for one-time setup or fetching initial data.
// Example: DataInit("@get('/api/data')")
func DataInit(format string, args ...any) H {
	return Data("init", fmt.Sprintf(format, args...))
}

// DataEffect runs an expression continuously when reactive data changes.
// Use for reacting to signal changes.
// Example: DataEffect("console.log($count)")
func DataEffect(format string, args ...any) H {
	return Data("effect", fmt.Sprintf(format, args...))
}

// DataIgnoreMorph tells Datastar to skip morphing this element during updates.
// Use when you want to manually control an element's DOM.
func DataIgnoreMorph() H {
	return Attr("data-ignore-morph")
}

// DataShow conditionally shows/hides an element based on a boolean expression.
// Example: DataShow("$count > 0")
func DataShow(format string, args ...any) H {
	return Data("show", fmt.Sprintf(format, args...))
}

// DataText sets the text content of an element from a signal.
// Only useful for frontend-only signals (prefixed with _).
// For normal signals, use via.Signal().Text() instead.
// Example: DataText("_name")
func DataText(format string, args ...any) H {
	return Data("text", fmt.Sprintf(format, args...))
}

// DataOnClick creates a click event handler for Datastar.
// The expression is executed when the element is clicked.
// Example: DataOnClick("$count = $count + 1")
func DataOnClick(format string, args ...any) H {
	return Data("on:click", fmt.Sprintf(format, args...))
}

// DataOnChange creates a change event handler for Datastar.
// The expression is executed when the element's value changes.
// Example: DataOnChange("$value = $event.target.value")
func DataOnChange(format string, args ...any) H {
	return Data("on:change", fmt.Sprintf(format, args...))
}

// DataBind creates a two-way binding for form elements with frontend-only signals (prefixed with _).
// The signal value is synced with the element's value.
// For normal server-side signals, use via.Signal().Bind() instead.
// Example: DataBind("_theme")
func DataBind(format string, args ...any) H {
	return Data("bind", fmt.Sprintf(format, args...))
}

// DataSignals creates signal initializations for frontend-only signals (prefixed with _).
// These signals exist only in the browser and are not synced with the server.
// For normal server-side signals, use via.Signal() instead.
// The value should be a JSON object with signal names and initial values.
// Example: DataSignals(`{"_theme": "blue", "_count": 0}`)
func DataSignals(format string, args ...any) H {
	return Data("signals", fmt.Sprintf(format, args...))
}

// DataAttr dynamically sets any HTML attribute based on a reactive expression.
// Replace * with the attribute name (e.g., DataAttr("href", "$url")).
// Example: DataAttr("href", "$url")
func DataAttr(name, format string, args ...any) H {
	return Data("attr:"+name, fmt.Sprintf(format, args...))
}

// DataClass conditionally adds/removes CSS classes based on a boolean expression.
// Example: DataClass("active", "$isActive")
func DataClass(className, format string, args ...any) H {
	return Data("class:"+className, fmt.Sprintf(format, args...))
}

// DataStyle dynamically sets inline styles based on a reactive expression.
// Example: DataStyle("color", "$textColor")
func DataStyle(property, format string, args ...any) H {
	return Data("style:"+property, fmt.Sprintf(format, args...))
}

// DataOnInput handles input events for text input elements.
// The expression is executed when the input value changes.
// Example: DataOnInput("$value = $event.target.value")
func DataOnInput(format string, args ...any) H {
	return Data("on:input", fmt.Sprintf(format, args...))
}

// DataOnSubmit handles form submit events.
// The expression is executed when the form is submitted.
// Example: DataOnSubmit("@post('/api/submit')")
func DataOnSubmit(format string, args ...any) H {
	return Data("on:submit", fmt.Sprintf(format, args...))
}

// DataOnKeyDown handles keydown events.
// Example: DataOnKeyDown("$key = $event.key")
func DataOnKeyDown(format string, args ...any) H {
	return Data("on:keydown", fmt.Sprintf(format, args...))
}

// DataOnKeyUp handles keyup events.
// Example: DataOnKeyUp("$key = $event.key")
func DataOnKeyUp(format string, args ...any) H {
	return Data("on:keyup", fmt.Sprintf(format, args...))
}

// DataOnFocus handles focus events.
// Example: DataOnFocus("$focused = true")
func DataOnFocus(format string, args ...any) H {
	return Data("on:focus", fmt.Sprintf(format, args...))
}

// DataOnBlur handles blur events (when element loses focus).
// Example: DataOnBlur("$focused = false")
func DataOnBlur(format string, args ...any) H {
	return Data("on:blur", fmt.Sprintf(format, args...))
}

// DataOnMouseOver handles mouseenter events.
// Example: DataOnMouseOver("$hover = true")
func DataOnMouseOver(format string, args ...any) H {
	return Data("on:mouseover", fmt.Sprintf(format, args...))
}

// DataOnMouseOut handles mouseleave events.
// Example: DataOnMouseOut("$hover = false")
func DataOnMouseOut(format string, args ...any) H {
	return Data("on:mouseout", fmt.Sprintf(format, args...))
}
