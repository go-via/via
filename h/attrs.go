package h

import (
	"fmt"
	"strings"

	"github.com/go-via/via/internal/hcore"
)

// This file is the typed attribute vocabulary. Every helper here is a thin
// wrapper over RawAttr or hcore.BoolAttr with the name spelled once, in one
// place, correctly: h.Placeholder("name") cannot be misspelled the way
// h.RawAttr("placholder", "name") can, and the compiler catches the difference.
// URL-valued attributes (Href, Src, Action) live in url.go because they carry a
// scheme gate as well as a name.
//
// Two shapes, and the distinction matters:
//
//   - value attributes render name="val", with val HTML-escaped at render time.
//   - boolean attributes render as a bare name when on and as NOTHING when off,
//     because `disabled="false"` disables a control in every browser. That
//     absence is the whole reason these are not RawAttr calls.

// stringish renders any Stringish value the way Str does, so Value(7) and
// Value("7") produce the same attribute.
func stringish[T Stringish](v T) string { return fmt.Sprint(v) }

// ID is the element id. Give a row a stable id when the list can reorder or
// shrink, or the live patch will morph by position instead.
func ID(id string) Attr { return RawAttr("id", id) }

// Class joins the given class names with spaces, skipping empty ones, and
// renders nothing at all when none survive — so Class(a, b) with both empty
// leaves no stray class="" behind, and a conditional class is just an empty
// string at the call site.
func Class(names ...string) Attr {
	kept := make([]string, 0, len(names))
	for _, n := range names {
		if n != "" {
			kept = append(kept, n)
		}
	}
	if len(kept) == 0 {
		return hcore.NoAttr()
	}
	return RawAttr("class", strings.Join(kept, " "))
}

// Style is an inline style declaration. The value is HTML-escaped, so it cannot
// break out of the attribute, but it is NOT parsed as CSS: this package does
// not vet declarations. Prefer a class.
func Style(css string) Attr { return RawAttr("style", css) }

// Type is the type attribute — input type, button type, script type.
func Type(t string) Attr { return RawAttr("type", t) }

// Name is the form field name, the key the value arrives under in a POST.
func Name(n string) Attr { return RawAttr("name", n) }

// Value is the form field value, from any Stringish value.
func Value[T Stringish](v T) Attr { return RawAttr("value", stringish(v)) }

// Placeholder is the input placeholder text.
func Placeholder(s string) Attr { return RawAttr("placeholder", s) }

// Title is the advisory title, shown as a tooltip. Not the <title> element.
func Title(s string) Attr { return RawAttr("title", s) }

// Alt is the alternative text for an image or area. Give every img one.
func Alt(s string) Attr { return RawAttr("alt", s) }

// Rel is the link relationship — "stylesheet", "noopener noreferrer".
func Rel(s string) Attr { return RawAttr("rel", s) }

// For binds a label to the id of the control it labels.
func For(id string) Attr { return RawAttr("for", id) }

// Target is the browsing context a link or form opens in. Pair Target("_blank")
// with Rel("noopener noreferrer").
func Target(s string) Attr { return RawAttr("target", s) }

// Lang is the language tag for the subtree.
func Lang(s string) Attr { return RawAttr("lang", s) }

// Role is the ARIA role.
func Role(s string) Attr { return RawAttr("role", s) }

// Method is the form submission method — "get" or "post".
func Method(s string) Attr { return RawAttr("method", s) }

// Enctype is the form encoding. A form with a file input needs
// Enctype("multipart/form-data").
func Enctype(s string) Attr { return RawAttr("enctype", s) }

// Accept is the accepted file types for a file input — ".png,image/jpeg".
func Accept(s string) Attr { return RawAttr("accept", s) }

// AutoComplete is the autocomplete hint — "off", "current-password".
func AutoComplete(s string) Attr { return RawAttr("autocomplete", s) }

// Width is the width attribute, from any Stringish value.
func Width[T Stringish](v T) Attr { return RawAttr("width", stringish(v)) }

// Height is the height attribute, from any Stringish value.
func Height[T Stringish](v T) Attr { return RawAttr("height", stringish(v)) }

// Min is the minimum for a number, range or date input.
func Min[T Stringish](v T) Attr { return RawAttr("min", stringish(v)) }

// Max is the maximum for a number, range or date input.
func Max[T Stringish](v T) Attr { return RawAttr("max", stringish(v)) }

// Step is the granularity for a number, range or date input.
func Step[T Stringish](v T) Attr { return RawAttr("step", stringish(v)) }

// Rows is the visible row count of a textarea.
func Rows(n int) Attr { return RawAttr("rows", stringish(n)) }

// Cols is the visible column count of a textarea.
func Cols(n int) Attr { return RawAttr("cols", stringish(n)) }

// MaxLength is the maximum accepted input length. Enforced by the browser only:
// re-check on the server.
func MaxLength(n int) Attr { return RawAttr("maxlength", stringish(n)) }

// MinLength is the minimum accepted input length, with the same caveat.
func MinLength(n int) Attr { return RawAttr("minlength", stringish(n)) }

// ColSpan is the number of columns a cell spans.
func ColSpan(n int) Attr { return RawAttr("colspan", stringish(n)) }

// RowSpan is the number of rows a cell spans.
func RowSpan(n int) Attr { return RawAttr("rowspan", stringish(n)) }

// TabIndex is the tab order position. -1 removes the element from tab order.
func TabIndex(n int) Attr { return RawAttr("tabindex", stringish(n)) }

// Boolean attributes. Each renders as a bare name when on and emits nothing
// when off — never disabled="false", which disables the control.

// Disabled disables a form control when on.
func Disabled(on bool) Attr { return hcore.BoolAttr("disabled", on) }

// Checked pre-checks a checkbox or radio when on.
func Checked(on bool) Attr { return hcore.BoolAttr("checked", on) }

// Required marks a control required when on. Browser-side only: validate on the
// server too.
func Required(on bool) Attr { return hcore.BoolAttr("required", on) }

// ReadOnly makes a control read-only when on. Unlike Disabled, its value is
// still submitted.
func ReadOnly(on bool) Attr { return hcore.BoolAttr("readonly", on) }

// Selected pre-selects an option when on.
func Selected(on bool) Attr { return hcore.BoolAttr("selected", on) }

// Multiple allows multiple values on a select or file input when on.
func Multiple(on bool) Attr { return hcore.BoolAttr("multiple", on) }

// AutoFocus focuses the control on load when on. Use at most one per page.
func AutoFocus(on bool) Attr { return hcore.BoolAttr("autofocus", on) }

// Hidden hides the element when on.
func Hidden(on bool) Attr { return hcore.BoolAttr("hidden", on) }

// Open expands a details or dialog element when on.
func Open(on bool) Attr { return hcore.BoolAttr("open", on) }

// NoValidate skips the browser's own form validation when on.
func NoValidate(on bool) Attr { return hcore.BoolAttr("novalidate", on) }

// Async loads a script asynchronously when on.
func Async(on bool) Attr { return hcore.BoolAttr("async", on) }

// Defer defers script execution until after parsing when on.
func Defer(on bool) Attr { return hcore.BoolAttr("defer", on) }

// Inert makes the subtree non-interactive when on.
func Inert(on bool) Attr { return hcore.BoolAttr("inert", on) }

// Loop restarts media playback when on.
func Loop(on bool) Attr { return hcore.BoolAttr("loop", on) }

// Muted mutes media when on.
func Muted(on bool) Attr { return hcore.BoolAttr("muted", on) }

// Controls shows native media controls when on.
func Controls(on bool) Attr { return hcore.BoolAttr("controls", on) }

// PlaysInline plays video inline rather than fullscreen when on.
func PlaysInline(on bool) Attr { return hcore.BoolAttr("playsinline", on) }

// Reversed numbers an ordered list descending when on.
func Reversed(on bool) Attr { return hcore.BoolAttr("reversed", on) }
