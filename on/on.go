// Package on builds reactive event-handler attributes that POST to via
// actions. It reads at the call site like HTML:
//
//	h.Button(h.Text("+"), on.Click(c.Inc))
//	h.Form(h.Input(...), on.Submit(c.Save))
//	h.Input(on.Input(c.Filter, on.Debounce("200ms")))
//	h.Div(on.Key("Enter", c.Send))
//
// Pass a bound method value of signature `func(*via.Ctx) error` or
// `func(*via.Ctx)` (drop the error when nothing in the body can fail).
// The method name is resolved via runtime reflection on the closure's
// PC; the rendered attribute issues a Datastar `@post('/_action/<method>')`.
package on

import (
	"encoding/json"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// eventAttrCache pre-computes the "on:<event>" attribute name for every
// event the on package knows about. Lookups skip the runtime "on:" + name
// concat — the resulting string already lives in the binary.
var eventAttrCache = map[string]string{
	"click":      "on:click",
	"change":     "on:change",
	"input":      "on:input",
	"submit":     "on:submit",
	"focus":      "on:focus",
	"blur":       "on:blur",
	"dblclick":   "on:dblclick",
	"mouseenter": "on:mouseenter",
	"mouseleave": "on:mouseleave",
	"load":       "on:load",
}

// Click binds a click handler.
func Click(fn via.ActionFn, opts ...via.TriggerOption) h.H {
	return event("click", fn, opts...)
}

// Change binds a change handler (e.g. <select>, <input type=checkbox>).
func Change(fn via.ActionFn, opts ...via.TriggerOption) h.H {
	return event("change", fn, opts...)
}

// Input binds an input handler.
func Input(fn via.ActionFn, opts ...via.TriggerOption) h.H {
	return event("input", fn, opts...)
}

// Submit binds a form submit handler.
func Submit(fn via.ActionFn, opts ...via.TriggerOption) h.H {
	return event("submit", fn, opts...)
}

// Focus binds a focus handler.
func Focus(fn via.ActionFn, opts ...via.TriggerOption) h.H {
	return event("focus", fn, opts...)
}

// Blur binds a blur handler.
func Blur(fn via.ActionFn, opts ...via.TriggerOption) h.H {
	return event("blur", fn, opts...)
}

// DblClick binds a double-click handler.
func DblClick(fn via.ActionFn, opts ...via.TriggerOption) h.H {
	return event("dblclick", fn, opts...)
}

// MouseEnter binds a mouseenter handler (does not bubble).
func MouseEnter(fn via.ActionFn, opts ...via.TriggerOption) h.H {
	return event("mouseenter", fn, opts...)
}

// MouseLeave binds a mouseleave handler (does not bubble).
func MouseLeave(fn via.ActionFn, opts ...via.TriggerOption) h.H {
	return event("mouseleave", fn, opts...)
}

// Load fires the action once when Datastar evaluates the attribute on
// the element — useful for kicking off a refresh as soon as a fragment
// appears in the DOM:
//
//	h.Div(on.Load(p.RefreshChart))
func Load(fn via.ActionFn, opts ...via.TriggerOption) h.H {
	return event("load", fn, opts...)
}

// Event is the escape hatch for any DOM event not covered by a named
// helper above. Pass the event name as it would appear after `on:`
// (e.g. "scroll", "wheel", "contextmenu"):
//
//	h.Div(on.Event("scroll", p.OnScroll, on.Throttle("100ms")))
func Event(name string, fn via.ActionFn, opts ...via.TriggerOption) h.H {
	return event(name, fn, opts...)
}

// Key binds a keydown handler that fires only when the named key matches.
// "Enter", "Escape", "ArrowUp", … (W3C key codes).
func Key(key string, fn via.ActionFn, opts ...via.TriggerOption) h.H {
	spec := &via.TriggerSpec{
		Event:     "keydown",
		Method:    fn,
		KeyFilter: key,
	}
	for _, o := range opts {
		o(spec)
	}
	return render(spec)
}

// Debounce returns a TriggerOption that debounces firing.
func Debounce(d string) via.TriggerOption {
	return func(s *via.TriggerSpec) { s.Debounce = d }
}

// Throttle returns a TriggerOption that throttles firing.
func Throttle(d string) via.TriggerOption {
	return func(s *via.TriggerSpec) { s.Throttle = d }
}

// preventFn / stopFn are pre-allocated TriggerOption closures so each
// `on.Click(fn, on.Prevent())` call doesn't allocate a fresh closure. The
// only state captured is the modifier name, which is constant.
var (
	preventFn via.TriggerOption = func(s *via.TriggerSpec) { s.Modifiers = append(s.Modifiers, "prevent") }
	stopFn    via.TriggerOption = func(s *via.TriggerSpec) { s.Modifiers = append(s.Modifiers, "stop") }
)

// Prevent calls e.preventDefault() before invoking the action.
func Prevent() via.TriggerOption { return preventFn }

// Stop calls e.stopPropagation() before invoking the action.
func Stop() via.TriggerOption { return stopFn }

// SetSignal bundles a typed signal write into the same trigger as the
// action — the signal updates client-side first, then the @post fires
// (and reads the new value):
//
//	h.Button(h.Text("Step 5"),
//	    on.Click(c.Apply, on.SetSignal(&c.Step, 5)),
//	)
//
// sig must be a Signal[T] handle bound at Mount (any Signal[T] field
// reached through the composition struct satisfies this). value is
// JSON-encoded into the rendered JS expression.
func SetSignal[T any](sig *via.Signal[T], value T) via.TriggerOption {
	encoded, err := json.Marshal(value)
	if err != nil {
		// json.Marshal on a typed Signal[T] value only fails for T's that
		// can't be JSON-encoded at all (channels, funcs, unsafe.Pointer).
		// That's a programmer error, not a runtime condition — make it
		// loud so it's caught at first render.
		panic("on.SetSignal: signal " + sig.Key() + " value cannot be JSON-encoded: " + err.Error())
	}
	stmt := "$" + sig.Key() + "=" + string(encoded)
	return func(s *via.TriggerSpec) { s.AppendPre(stmt) }
}

func event(name string, fn via.ActionFn, opts ...via.TriggerOption) h.H {
	// Fast path for the bare `on.Click(c.Inc)` shape — no opts means no
	// modifiers, no debounce/throttle, no pre-statements. Skipping the
	// TriggerSpec allocation here pairs with render's same-shape fast
	// path; together they keep zero-option bindings allocation-cheap.
	if len(opts) == 0 {
		method := via.MethodName(fn)
		if method == "" {
			return nil
		}
		attr, ok := eventAttrCache[name]
		if !ok {
			attr = "on:" + name
		}
		return h.Data(attr, "@post('/_action/"+method+"')")
	}
	spec := &via.TriggerSpec{Event: name, Method: fn}
	for _, o := range opts {
		o(spec)
	}
	return render(spec)
}

func render(s *via.TriggerSpec) h.H {
	method := via.MethodName(s.Method)
	if method == "" {
		return nil
	}

	// Fast path for the bare `on.Click(c.Inc)` shape — no modifiers, no
	// key filter, no debounce/throttle, no pre statements. By far the
	// common case; skipping two strings.Builder allocations per render
	// per binding adds up across a moderately interactive view.
	if len(s.Pre) == 0 && len(s.Modifiers) == 0 &&
		s.KeyFilter == "" && s.Debounce == "" && s.Throttle == "" {
		attr, ok := eventAttrCache[s.Event]
		if !ok {
			attr = "on:" + s.Event
		}
		return h.Data(attr, "@post('/_action/"+method+"')")
	}

	var attr strings.Builder
	attr.WriteString("on:")
	attr.WriteString(s.Event)
	if s.KeyFilter != "" {
		attr.WriteByte('.')
		attr.WriteString(s.KeyFilter)
	}
	for _, m := range s.Modifiers {
		attr.WriteByte('.')
		attr.WriteString(m)
	}
	if s.Debounce != "" {
		attr.WriteString(".debounce.")
		attr.WriteString(s.Debounce)
	}
	if s.Throttle != "" {
		attr.WriteString(".throttle.")
		attr.WriteString(s.Throttle)
	}

	var expr strings.Builder
	for _, stmt := range s.Pre {
		expr.WriteString(stmt)
		expr.WriteByte(';')
	}
	expr.WriteString("@post('/_action/")
	expr.WriteString(method)
	expr.WriteString("')")
	return h.Data(attr.String(), expr.String())
}
