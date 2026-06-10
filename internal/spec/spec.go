// Package spec holds the action-trigger plumbing types shared by the
// via and via/on packages. It exists so the user-facing via package
// doesn't have to export them: 3rd-party event helpers are rare, and
// the via/on package is the only first-party consumer.
package spec

import (
	"reflect"
	"runtime"
	"strings"
	"sync"
)

// methodNameCache memoises [MethodName] results by trampoline PC so the
// reflect-light parse (runtime.FuncForPC, two string scans) only runs the
// first time we see a given bound method. PC is stable per (type, method)
// pair, so the cache is safe across compositions.
var methodNameCache sync.Map // map[uintptr]string

// MethodName resolves a bound method value (like `c.Inc`) to its method
// name. Method values in Go are closures; runtime.FuncForPC on the value's
// PC returns the trampoline name in the form
//
//	pkg.(*Counter).Inc-fm
//
// We require the trailing "-fm" suffix so anonymous closures and
// top-level functions — which have no such suffix — return "" rather
// than a wrong-looking name like "func1" or "myHandler". Strip the
// package, receiver, and "-fm" suffix to recover "Inc". Returns "" if
// the function value is not recognizable as a bound method.
//
// The "-fm" suffix is a Go runtime internal, not a language contract.
// A canary in the via package ([VerifyTrampoline]) fires loudly at App
// construction if a Go toolchain upgrade changes the trampoline naming.
func MethodName(fn any) string {
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.Kind() != reflect.Func {
		return ""
	}
	pc := v.Pointer()
	if cached, ok := methodNameCache.Load(pc); ok {
		return cached.(string)
	}
	fnPC := runtime.FuncForPC(pc)
	if fnPC == nil {
		return ""
	}
	full := fnPC.Name()
	if !strings.HasSuffix(full, "-fm") {
		methodNameCache.Store(pc, "")
		return ""
	}
	full = strings.TrimSuffix(full, "-fm")
	if i := strings.LastIndex(full, "."); i >= 0 {
		full = full[i+1:]
	}
	methodNameCache.Store(pc, full)
	return full
}

// Option layers extra behaviour onto a Trigger (debounce, throttle, key
// filters, etc.). The via/on package exposes user-facing builders that
// produce Options.
type Option func(*Trigger)

// Trigger is the resolved configuration of one event binding.
//
// Method is a bound method value of either `func(*via.Ctx)` or
// `func(*via.Ctx) error`. The runtime resolves the method name via
// [MethodName] and dispatches via reflect — both shapes are accepted;
// nothing else is.
type Trigger struct {
	Event     string // "click", "input", "submit", …
	Method    any    // bound method value — see godoc above
	Debounce  string // e.g. "200ms"
	Throttle  string
	Modifiers []string // e.g. ["prevent", "stop"]
	KeyFilter string   // e.g. "Enter" for on:keydown

	// Confirm, when non-empty, is a JSON-encoded string used as a
	// confirm(<Confirm>) guard that short-circuits the action POST unless
	// the user accepts. Set by on.Confirm.
	Confirm string

	// Pre is a list of JS statements to run synchronously before the
	// @post(...) call fires. Used by on.SetSignal to bundle a typed
	// signal write into the same trigger.
	Pre []string
}

// AppendPre adds a JS statement that will run before the action POST.
// Used by on.SetSignal and other helpers in the on/* package; the
// statements run in insertion order.
func (s *Trigger) AppendPre(stmt string) {
	if stmt == "" {
		return
	}
	s.Pre = append(s.Pre, stmt)
}
