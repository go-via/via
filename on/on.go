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
	"html/template"
	"reflect"
	"runtime"
	"strings"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/spec"
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
func Click[F via.Action](fn F, opts ...spec.Option) h.H { return event("click", fn, opts...) }

// Change binds a change handler (e.g. <select>, <input type=checkbox>).
func Change[F via.Action](fn F, opts ...spec.Option) h.H { return event("change", fn, opts...) }

// Input binds an input handler.
func Input[F via.Action](fn F, opts ...spec.Option) h.H { return event("input", fn, opts...) }

// Submit binds a form submit handler.
func Submit[F via.Action](fn F, opts ...spec.Option) h.H { return event("submit", fn, opts...) }

// Focus binds a focus handler.
func Focus[F via.Action](fn F, opts ...spec.Option) h.H { return event("focus", fn, opts...) }

// Blur binds a blur handler.
func Blur[F via.Action](fn F, opts ...spec.Option) h.H { return event("blur", fn, opts...) }

// DblClick binds a double-click handler.
func DblClick[F via.Action](fn F, opts ...spec.Option) h.H { return event("dblclick", fn, opts...) }

// MouseEnter binds a mouseenter handler (does not bubble).
func MouseEnter[F via.Action](fn F, opts ...spec.Option) h.H {
	return event("mouseenter", fn, opts...)
}

// MouseLeave binds a mouseleave handler (does not bubble).
func MouseLeave[F via.Action](fn F, opts ...spec.Option) h.H {
	return event("mouseleave", fn, opts...)
}

// Load fires the action once when Datastar evaluates the attribute on
// the element — useful for kicking off a refresh as soon as a fragment
// appears in the DOM:
//
//	h.Div(on.Load(p.RefreshChart))
func Load[F via.Action](fn F, opts ...spec.Option) h.H { return event("load", fn, opts...) }

// Event is the escape hatch for any DOM event not covered by a named
// helper above. Pass the event name as it would appear after `on:`
// (e.g. "scroll", "wheel", "contextmenu"):
//
//	h.Div(on.Event("scroll", p.OnScroll, on.Throttle("100ms")))
//
// name should be a compile-time constant string. The bare-binding cache
// keys on (event, method) and is never evicted, so deriving name from
// user input or per-request data would grow the cache unboundedly. The
// cache is sized correctly when call sites are static — tens to
// hundreds of bindings for any real app.
func Event[F via.Action](name string, fn F, opts ...spec.Option) h.H {
	return event(name, fn, opts...)
}

// Key binds a keydown handler that fires only when the named key matches.
// "Enter", "Escape", "ArrowUp", … (W3C key codes).
func Key[F via.Action](key string, fn F, opts ...spec.Option) h.H {
	spec := &spec.Trigger{
		Event:     "keydown",
		Method:    fn,
		KeyFilter: key,
	}
	for _, o := range opts {
		o(spec)
	}
	return render(spec)
}

// Debounce returns a trigger option that debounces firing.
func Debounce(d string) spec.Option { return func(s *spec.Trigger) { s.Debounce = d } }

// Throttle returns a trigger option that throttles firing.
func Throttle(d string) spec.Option { return func(s *spec.Trigger) { s.Throttle = d } }

// preventFn / stopFn are pre-allocated trigger-option closures so each
// `on.Click(fn, on.Prevent())` call doesn't allocate a fresh closure. The
// only state captured is the modifier name, which is constant.
var (
	preventFn spec.Option = func(s *spec.Trigger) { s.Modifiers = append(s.Modifiers, "prevent") }
	stopFn    spec.Option = func(s *spec.Trigger) { s.Modifiers = append(s.Modifiers, "stop") }
)

// Prevent calls e.preventDefault() before invoking the action.
func Prevent() spec.Option { return preventFn }

// Stop calls e.stopPropagation() before invoking the action.
func Stop() spec.Option { return stopFn }

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
func SetSignal[T any](sig *via.Signal[T], value T) spec.Option {
	encoded, err := json.Marshal(value)
	if err != nil {
		// json.Marshal on a typed Signal[T] value only fails for T's that
		// can't be JSON-encoded at all (channels, funcs, unsafe.Pointer).
		// That's a programmer error, not a runtime condition — make it
		// loud so it's caught at first render.
		panic("on.SetSignal: signal " + sig.Key() + " value cannot be JSON-encoded: " + err.Error())
	}
	stmt := "$" + sig.Key() + "=" + string(encoded)
	return func(s *spec.Trigger) { s.AppendPre(stmt) }
}

// notMethodPanic builds the panic text for an on.* helper that received
// something other than a bound method value. Splitting nil / top-level
// function / closure makes the most common authoring mistake debuggable
// at a glance — the previous lumped message left the dev to guess which
// of the three they did.
func notMethodPanic(eventName string, fn any) string {
	prefix := "on: " + eventName + " requires a bound method value (e.g. on.Click(c.Inc)); got "
	if fn == nil {
		return prefix + "nil"
	}
	v := reflect.ValueOf(fn)
	if !v.IsValid() {
		return prefix + "nil"
	}
	if v.Kind() != reflect.Func {
		return prefix + "a non-function value of type " + v.Type().String()
	}
	if v.IsNil() {
		return prefix + "nil"
	}
	fnPC := runtime.FuncForPC(v.Pointer())
	if fnPC == nil {
		return prefix + "a closure"
	}
	if isClosureName(fnPC.Name()) {
		return prefix + "a closure"
	}
	return prefix + "a top-level function"
}

// isClosureName reports whether name is a Go runtime closure trampoline.
// The runtime names anonymous functions `outerName.funcN[.M…]` with a
// digit immediately after `.func`; a substring match on `.func` alone
// would catch top-level functions whose identifier begins with "func"
// (e.g. `pkg.function`).
func isClosureName(name string) bool {
	for i := 0; i+5 < len(name); i++ {
		if name[i:i+5] != ".func" {
			continue
		}
		c := name[i+5]
		if c >= '0' && c <= '9' {
			return true
		}
	}
	return false
}

func event(name string, fn any, opts ...spec.Option) h.H {
	// Fast path for the bare `on.Click(c.Inc)` shape — no opts means no
	// modifiers, no debounce/throttle, no pre-statements. Skipping the
	// spec.Trigger allocation here pairs with render's same-shape fast
	// path; together they keep zero-option bindings allocation-cheap.
	if len(opts) == 0 {
		method := spec.MethodName(fn)
		if method == "" {
			panic(notMethodPanic(name, fn))
		}
		return bareAttr(name, method)
	}
	spec := &spec.Trigger{Event: name, Method: fn}
	for _, o := range opts {
		o(spec)
	}
	return render(spec)
}

// bareAttrCache memoises the h.H produced for each (event, method) pair so
// every render of `on.Click(c.Inc)` reuses one interned node instead of
// rebuilding the @post string and a fresh attribute node. Hits are
// allocation-free; misses pay the original cost once. A typed map under
// RWMutex (rather than sync.Map) avoids boxing the struct key into `any`
// on every lookup — the boxing alloc dominates after the closure is gone.
//
// Never evicted: the map is bounded by the number of distinct
// (event, method) bindings the application uses, which is statically
// determined by call sites — tens to hundreds for any real codebase.
var (
	bareAttrMu    sync.RWMutex
	bareAttrCache = map[bareKey]h.H{}
)

type bareKey struct{ event, method string }

// bareAttr emits the data-on:<event>="@post('/_action/<method>')"
// attribute used by every binding that has no modifiers, key filter,
// debounce/throttle, or pre statements. Shared by event's and render's
// fast paths. The cached value is a precomputed []byte that Render
// writes verbatim — building a fresh attribute node and re-escaping
// every render would be wasted work since the @post expression is
// constant per (event, method).
func bareAttr(eventName, method string) h.H {
	key := bareKey{eventName, method}
	bareAttrMu.RLock()
	cached, ok := bareAttrCache[key]
	bareAttrMu.RUnlock()
	if ok {
		return cached
	}
	attr, ok := eventAttrCache[eventName]
	if !ok {
		attr = "on:" + eventName
	}
	expr := "@post('/_action/" + method + "')"
	// Pre-render: leading space + data-on:... + ="<escaped expr>". Matches
	// the renderer's attribute output byte-for-byte.
	escaped := template.HTMLEscapeString(expr)
	bytes := make([]byte, 0, len(" data-")+len(attr)+len(`="`)+len(escaped)+1)
	bytes = append(bytes, " data-"...)
	bytes = append(bytes, attr...)
	bytes = append(bytes, `="`...)
	bytes = append(bytes, escaped...)
	bytes = append(bytes, '"')
	node := h.H(h.RawAttr(bytes))
	bareAttrMu.Lock()
	if existing, ok := bareAttrCache[key]; ok {
		node = existing
	} else {
		bareAttrCache[key] = node
	}
	bareAttrMu.Unlock()
	return node
}

func render(s *spec.Trigger) h.H {
	method := spec.MethodName(s.Method)
	if method == "" {
		panic(notMethodPanic(s.Event, s.Method))
	}

	// Fast path for the bare `on.Click(c.Inc)` shape — no modifiers, no
	// key filter, no debounce/throttle, no pre statements. By far the
	// common case; skipping two strings.Builder allocations per render
	// per binding adds up across a moderately interactive view.
	if len(s.Pre) == 0 && len(s.Modifiers) == 0 &&
		s.KeyFilter == "" && s.Debounce == "" && s.Throttle == "" {
		return bareAttr(s.Event, method)
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
	// Emit pre-escaped bytes so Render writes them verbatim — same trick
	// as bareAttr. The optioned path is non-cached (every spec.Trigger
	// shape is bespoke), but skipping per-render escaping still wins
	// because the binding is rendered once per View call.
	escaped := template.HTMLEscapeString(expr.String())
	name := attr.String()
	buf := make([]byte, 0, len(" data-")+len(name)+len(`="`)+len(escaped)+1)
	buf = append(buf, " data-"...)
	buf = append(buf, name...)
	buf = append(buf, `="`...)
	buf = append(buf, escaped...)
	buf = append(buf, '"')
	return h.RawAttr(buf)
}
