package via

import (
	"reflect"
	"runtime"
	"strings"
)

// MethodName resolves a bound method value (like `c.Inc`) to its method
// name. Method values in Go are closures; runtime.FuncForPC on the value's
// PC returns the trampoline name in the form
//
//	pkg.(*Counter).Inc-fm
//
// We strip the package, receiver, and -fm suffix to recover "Inc". Returns
// "" if the function value is not recognizable as a bound method.
func MethodName(fn any) string {
	v := reflect.ValueOf(fn)
	if !v.IsValid() || v.Kind() != reflect.Func {
		return ""
	}
	pc := runtime.FuncForPC(v.Pointer())
	if pc == nil {
		return ""
	}
	full := pc.Name()
	// Trim trailing "-fm" (Go's bound-method suffix).
	full = strings.TrimSuffix(full, "-fm")
	// Last dot separates receiver/package from method name.
	if i := strings.LastIndex(full, "."); i >= 0 {
		return full[i+1:]
	}
	return full
}

// ActionFn is the function-shape every action method satisfies.
type ActionFn = func(ctx *Ctx) error

// TriggerOption is consumed by the on/* sub-package to layer extra
// behaviour onto a binding (debounce, throttle, key filters, etc.).
type TriggerOption func(*TriggerSpec)

// TriggerSpec is the resolved configuration of one event binding. The on/*
// package exposes a builder; via owns the type so consumers don't need to
// reach across packages.
type TriggerSpec struct {
	Event     string // "click", "input", "submit", …
	Method    ActionFn
	Debounce  string // e.g. "200ms"
	Throttle  string
	Modifiers []string // e.g. ["prevent", "stop"]
	KeyFilter string   // e.g. "Enter" for on:keydown

	// Pre is a list of JS statements to run synchronously before the
	// @post(...) call fires. Used by on.SetSignal to bundle a typed
	// signal write into the same trigger.
	Pre []string
}

// AppendPre adds a JS statement that will run before the action POST.
// Used by on.SetSignal and other helpers in the on/* package; the
// statements run in insertion order.
func (s *TriggerSpec) AppendPre(stmt string) {
	if stmt == "" {
		return
	}
	s.Pre = append(s.Pre, stmt)
}
