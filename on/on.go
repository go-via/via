// Package on builds reactive event-handler attributes that POST to via
// actions. It reads at the call site like HTML:
//
//	h.Button(h.Text("+"), on.Click(c.Inc))
//	h.Form(h.Input(...), on.Submit(c.Save))
//	h.Input(on.Input(c.Filter, on.Debounce("200ms")))
//	h.Div(on.Key("Enter", c.Send))
//
// Pass a bound method value of signature `func(*via.Ctx) error`. The method
// name is resolved via runtime reflection on the closure's PC; the rendered
// attribute issues a Datastar `@post('/_action/<method>')`.
package on

import (
	"encoding/json"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func jsonForJS(v any) (string, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(b), nil
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

// Prevent calls e.preventDefault() before invoking the action.
func Prevent() via.TriggerOption {
	return modifier("prevent")
}

// Stop calls e.stopPropagation() before invoking the action.
func Stop() via.TriggerOption {
	return modifier("stop")
}

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
	key := sig.Key()
	encoded, err := jsonForJS(value)
	if err != nil {
		// Fall back to a no-op so render doesn't blow up; the user will
		// see no signal write but the action still fires.
		return func(*via.TriggerSpec) {}
	}
	stmt := "$" + key + "=" + encoded
	return func(s *via.TriggerSpec) { s.AppendPre(stmt) }
}

func modifier(m string) via.TriggerOption {
	return func(s *via.TriggerSpec) { s.Modifiers = append(s.Modifiers, m) }
}

func event(name string, fn via.ActionFn, opts ...via.TriggerOption) h.H {
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
