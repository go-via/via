// Package on binds DOM events with the event name and its modifiers checked by
// the compiler: on.Event("clik", …) compiles and never fires, on.Click cannot
// be misspelled. Each event has a server form that posts an action (on.Click)
// and a client-only twin that runs an expression in the browser (on.ClickCS).
// [Event] and [EventCS] take any other event by name.
//
//	h.Button(on.Click(c.Inc), h.Str("+1"))
//	h.Input(on.Input(c.Search, on.WithDebounce(250*time.Millisecond)))
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
// once per row does the same; bind a method with [Bind] instead.
package on

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/expr"
	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/render"
)

// Bound is an action with its argument attached, made by [Bind].
type Bound struct {
	bind func(event string) h.Attr
}

// Bind attaches arg to fn, so the row's own datum rides with the event and the
// handler acts on that item regardless of its render position. fn is a named
// method value (l.Delete); arg is plain data (todo.ID), not an identifier
// string.
//
//	h.Button(on.Click(on.Bind(l.Delete, todo.ID)), h.Str("×"))
//
// DISPATCHABLE-IFF-RENDERED. Dispatch identity is the (handler,
// arg) pair. The render that precedes every dispatch — the discovery render on
// a plain page, the last push on a live one — rebuilds the set of args it
// binds for fn, and a POST whose ?a= is not in that set is 410'd before fn
// ever runs. So an arg the caller's own render does not produce (another
// user's row, an id behind a When branch closed for them) is not dispatchable
// by them, and fn may treat its parameter as already authorized. Keep the
// render honest — filter the list by the caller's identity — and the handler
// needs no check of its own.
//
// That render is server state alone: an inbound value is accepted only for a
// slot the render put under client control (Signal.Bind), and the body is
// applied after the discovery render, so a POST cannot open the branch that
// authorizes it. A bound signal IS client-controlled, so gating on one is the
// client's decision to make — an authorization gate belongs on session or
// database state, never on a signal.
//
// trap: arg must be a stable identity (a row's primary key), never a value
// that moves between renders. A pagination cursor or count bound as an arg
// goes stale the moment any push re-renders the button, and the click already
// in flight 410s. Read changing state from the composition in an argless
// handler instead.
func Bind[T any](fn func(*via.Ctx, T), arg T) Bound {
	decode := func(raw []byte) (any, error) {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, err
		}
		return func(rc *via.Ctx) { fn(rc, v) }, nil
	}
	return Bound{bind: func(event string) h.Attr {
		return render.DynAttr(func(r *render.Renderer) {
			if b := r.Binder(); b != nil {
				b.ArgAction(r, event, fn, arg, decode)
			}
		})
	}}
}

// Option is a Datastar event modifier.
type Option func(*config)

type config struct {
	debounce, throttle                     time.Duration
	once, prevent, stop, outside, onWindow bool
	raw                                    []string
}

// WithDebounce runs the handler once the event has stopped firing for d,
// rounded up to the next millisecond.
func WithDebounce(d time.Duration) Option {
	mustPositive("WithDebounce", d)
	return func(c *config) { c.debounce = setDuration("WithDebounce", c.debounce, d) }
}

// WithThrottle runs the handler at most once per d, rounded up to the next
// millisecond.
func WithThrottle(d time.Duration) Option {
	mustPositive("WithThrottle", d)
	return func(c *config) { c.throttle = setDuration("WithThrottle", c.throttle, d) }
}

// WithOnce removes the listener after its first run.
func WithOnce() Option { return func(c *config) { c.once = true } }

// WithPrevent calls preventDefault on the event.
func WithPrevent() Option { return func(c *config) { c.prevent = true } }

// WithStop calls stopPropagation on the event.
func WithStop() Option { return func(c *config) { c.stop = true } }

// WithOutside fires only for events whose target is outside the element.
func WithOutside() Option { return func(c *config) { c.outside = true } }

// WithWindow listens on window instead of the element.
func WithWindow() Option { return func(c *config) { c.onWindow = true } }

// WithModifier appends a Datastar modifier package on has no option for, such
// as "delay.300ms", "passive" or "viewtransition". raw is the modifier name
// and its "."-separated tags, without the leading "__". Prefer the typed
// options: a modifier they cover panics, naming the option.
func WithModifier(raw string) Option {
	if typed, ok := modeled[strings.SplitN(raw, ".", 2)[0]]; ok {
		panic(fmt.Sprintf("on: WithModifier(%q): use on.%s", raw, typed))
	}
	if !modifier.MatchString(raw) {
		panic(fmt.Sprintf(`on: WithModifier(%q) must be a lower-case modifier name, optionally followed by '.'-separated lower-case letters and digits (such as "delay.300ms")`, raw))
	}
	return func(c *config) { c.raw = append(c.raw, raw) }
}

// modifier is one segment of Datastar's "__"-split modifier list, which it
// splits again on '.' into a name and tags. Tags are held to the modifier
// grammar via's action binding accepts.
var modifier = regexp.MustCompile(`^[a-z]+(?:\.[a-z0-9]+)*$`)

var modeled = map[string]string{
	"debounce": "WithDebounce", "throttle": "WithThrottle", "once": "WithOnce",
	"outside": "WithOutside", "prevent": "WithPrevent", "stop": "WithStop", "window": "WithWindow",
}

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
	for _, m := range c.raw {
		event += "__" + m
	}
	return event
}

// millis rounds up to whole milliseconds: Datastar splits tags on '.', so
// "1.5ms" would read as the tags "1" and "5ms", and a positive d must not
// become 0.
func millis(d time.Duration) string {
	return strconv.FormatInt(int64((d+time.Millisecond-1)/time.Millisecond), 10) + "ms"
}

// Event posts fn on the named DOM event, for events without their own
// function here.
func Event[F func(*via.Ctx) | Bound](name string, fn F, opts ...Option) h.Attr {
	mustEventName(name)
	k := key(name, opts)
	switch f := any(fn).(type) {
	case func(*via.Ctx):
		return render.DynAttr(func(r *render.Renderer) {
			if b := r.Binder(); b != nil {
				b.Action(r, k, f)
			}
		})
	case Bound:
		if f.bind == nil {
			panic("on: zero Bound; build one with on.Bind")
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
// [WithDebounce].
func Input[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("input", fn, opts...)
}

// Change posts fn on change.
func Change[F func(*via.Ctx) | Bound](fn F, opts ...Option) h.Attr {
	return Event("change", fn, opts...)
}

// Submit posts fn on submit. Datastar prevents a wired form's default submit
// itself, so [WithPrevent] is not needed.
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

// Scroll posts fn on scroll; pair it with [WithThrottle].
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
