// Package on binds DOM events with the event name and its modifiers checked by
// the compiler: via.On("clik", …) compiles and never fires, on.Click cannot be
// misspelled. Each event has a server form that posts an action (on.Click) and
// a client-only twin that runs an expression in the browser (on.ClickCS).
// [Event] and [EventCS] take any other event by name.
//
//	h.Button(on.Click(c.Inc), h.Str("+1"))
//	h.Input(on.Input(c.Search, on.Debounce(250*time.Millisecond)))
//	h.Button(on.ClickCS(open.Ref().Toggle()), h.Str("menu"))
//
// An action is a pointer-receiver method of the composition or of one of its
// struct fields (c.Inc, c.Stats.Reset). Its id is the method's name plus that
// field's path, so it survives a rebuild and a field reorder. A value-receiver
// method has no address, so it works only where its type sits at one path: on
// the composition itself, or on a type exactly one field holds. Bound where
// the type sits at several fields or at none, or taken through an interface
// field, it panics at Mount, or on the first render if the zero value does not
// reach it, since via could not tell the buttons apart. A func literal bound
// once per row does the same; bind a method with [WithArg] instead.
package on

import (
	"fmt"
	"regexp"
	"strconv"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
)

// Bound is an action with its argument attached, made by [WithArg].
type Bound struct {
	bind func(event string) h.Attr
}

// WithArg attaches arg to fn, so the row's own datum rides with the event (see
// via.OnArg for why the arg must be a stable identity).
//
//	h.Button(on.Click(on.WithArg(l.Delete, todo.ID)), h.Str("×"))
func WithArg[T any](fn func(*via.Ctx, T), arg T) Bound {
	return Bound{bind: func(event string) h.Attr {
		//lint:ignore SA1019 on wraps the deprecated binders until v0.9 removes them
		return via.OnArg(event, fn, arg)
	}}
}

// Option is a Datastar event modifier.
type Option func(*config)

type config struct {
	debounce, throttle                     time.Duration
	once, prevent, stop, outside, onWindow bool
}

// Debounce runs the handler once the event has stopped firing for d.
func Debounce(d time.Duration) Option {
	mustPositive("Debounce", d)
	return func(c *config) { c.debounce = setDuration("Debounce", c.debounce, d) }
}

// Throttle runs the handler at most once per d.
func Throttle(d time.Duration) Option {
	mustPositive("Throttle", d)
	return func(c *config) { c.throttle = setDuration("Throttle", c.throttle, d) }
}

// Once removes the listener after its first run.
func Once() Option { return func(c *config) { c.once = true } }

// Prevent calls preventDefault on the event.
func Prevent() Option { return func(c *config) { c.prevent = true } }

// Stop calls stopPropagation on the event.
func Stop() Option { return func(c *config) { c.stop = true } }

// Outside fires only for events whose target is outside the element.
func Outside() Option { return func(c *config) { c.outside = true } }

// Window listens on window instead of the element.
func Window() Option { return func(c *config) { c.onWindow = true } }

func mustPositive(name string, d time.Duration) {
	if d <= 0 {
		panic(fmt.Sprintf("on: %s needs a positive duration, got %s", name, d))
	}
}

func setDuration(name string, have, d time.Duration) time.Duration {
	if have != 0 && have != d {
		panic(fmt.Sprintf("on: conflicting %s options %s and %s", name, have, d))
	}
	return d
}

// eventName admits no quote or space, which would break out of the attribute,
// and no "__", which Datastar would read as a modifier.
var eventName = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[:.-][a-z0-9]+)*$`)

func mustEventName(name string) {
	if !eventName.MatchString(name) {
		panic(fmt.Sprintf(`on: event name %q must be lower-case letters and digits, optionally joined by ':', '.' or '-' (such as "click" or "via:patch")`, name))
	}
}

// key spells the data-on suffix. Datastar reads modifiers as a set, so the
// fixed order only keeps the output deterministic.
func key(event string, opts []Option) string {
	var c config
	for _, o := range opts {
		o(&c)
	}
	if c.debounce > 0 {
		event += "__debounce." + millis(c.debounce)
	}
	if c.throttle > 0 {
		event += "__throttle." + millis(c.throttle)
	}
	for _, m := range []struct {
		on   bool
		name string
	}{{c.once, "once"}, {c.prevent, "prevent"}, {c.stop, "stop"}, {c.outside, "outside"}, {c.onWindow, "window"}} {
		if m.on {
			event += "__" + m.name
		}
	}
	return event
}

func millis(d time.Duration) string {
	return strconv.FormatFloat(float64(d)/float64(time.Millisecond), 'f', -1, 64) + "ms"
}

// Event posts fn on the named DOM event, for events without their own
// function here.
func Event[F func(*via.Ctx) | Bound](name string, fn F, opts ...Option) h.Attr {
	mustEventName(name)
	k := key(name, opts)
	switch f := any(fn).(type) {
	case func(*via.Ctx):
		//lint:ignore SA1019 on wraps the deprecated binders until v0.9 removes them
		return via.On(k, f)
	case Bound:
		if f.bind == nil {
			panic("on: zero Bound; build one with on.WithArg")
		}
		return f.bind(k)
	}
	panic("unreachable")
}

// EventCS runs e in the browser on the named DOM event, for events without
// their own function here.
func EventCS(name string, e expr.Expr, opts ...Option) h.Attr {
	mustEventName(name)
	return h.DataOn(key(name, opts), e)
}

// Click posts fn on click.
func Click[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("click", fn, opts...)
}

// DblClick posts fn on dblclick.
func DblClick[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("dblclick", fn, opts...)
}

// Input posts fn on input, which fires on every keystroke; pair it with
// [Debounce].
func Input[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("input", fn, opts...)
}

// Change posts fn on change.
func Change[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("change", fn, opts...)
}

// Submit posts fn on submit. Datastar prevents a wired form's default submit
// itself, so [Prevent] is not needed.
func Submit[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("submit", fn, opts...)
}

// Keydown posts fn on keydown.
func Keydown[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("keydown", fn, opts...)
}

// Keyup posts fn on keyup.
func Keyup[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("keyup", fn, opts...)
}

// Focus posts fn on focus.
func Focus[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("focus", fn, opts...)
}

// Blur posts fn on blur.
func Blur[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("blur", fn, opts...)
}

// Load posts fn on load.
func Load[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("load", fn, opts...)
}

// MouseEnter posts fn on mouseenter.
func MouseEnter[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("mouseenter", fn, opts...)
}

// MouseLeave posts fn on mouseleave.
func MouseLeave[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("mouseleave", fn, opts...)
}

// Scroll posts fn on scroll; pair it with [Throttle].
func Scroll[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("scroll", fn, opts...)
}

// ClickCS runs e in the browser on click.
func ClickCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("click", e, opts...) }

// DblClickCS runs e in the browser on dblclick.
func DblClickCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("dblclick", e, opts...) }

// InputCS runs e in the browser on input.
func InputCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("input", e, opts...) }

// ChangeCS runs e in the browser on change.
func ChangeCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("change", e, opts...) }

// SubmitCS runs e in the browser on submit.
func SubmitCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("submit", e, opts...) }

// KeydownCS runs e in the browser on keydown.
func KeydownCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("keydown", e, opts...) }

// KeyupCS runs e in the browser on keyup.
func KeyupCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("keyup", e, opts...) }

// FocusCS runs e in the browser on focus.
func FocusCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("focus", e, opts...) }

// BlurCS runs e in the browser on blur.
func BlurCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("blur", e, opts...) }

// LoadCS runs e in the browser on load.
func LoadCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("load", e, opts...) }

// MouseEnterCS runs e in the browser on mouseenter.
func MouseEnterCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("mouseenter", e, opts...) }

// MouseLeaveCS runs e in the browser on mouseleave.
func MouseLeaveCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("mouseleave", e, opts...) }

// ScrollCS runs e in the browser on scroll.
func ScrollCS(e expr.Expr, opts ...Option) h.Attr { return EventCS("scroll", e, opts...) }
