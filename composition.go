// Package via builds reactive web UIs from typed Go structs.
//
// A composition is a struct. Its fields declare reactive state (Signal[T],
// StateTab[T]) and path parameters (path:"name" tag). Its methods of signature
// func(*Ctx) error become server actions. View(*CtxR) h.H draws it (the
// render context is read-only, so a View cannot mutate state).
//
//	type Counter struct {
//	    Hits via.StateTab[int]
//	    Step via.Signal[int] `via:"step,init=1"`
//	}
//	func (c *Counter) Inc(ctx *via.Ctx) error {
//	    c.Hits.Update(ctx, func(n int) (int, error) { return n + c.Step.Read(ctx), nil})
//	    return nil
//	}
//	func (c *Counter) View(ctx *via.CtxR) h.H { ... }
//
//	app := via.New()
//	via.Mount[Counter](app, "/counter")
//	http.ListenAndServe(":3000", app)
package via

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/go-via/via/h"
)

// Composition is anything that renders a view from a read-only Ctx.
// Types whose pointer satisfies this interface are mountable.
type Composition interface {
	View(ctx *CtxR) h.H
}

// Initializer is the optional lifecycle hook that runs on the
// page-render request before View. Use it to seed reactive state from
// the request (cookies, query params), kick off OnInit-time fetches,
// or prepare any data View needs. A non-nil error is logged but does
// not abort the render.
//
// The framework discovers OnInit via reflection on the method name —
// satisfying this interface is not required, but declaring it on the
// composition makes the hook self-documenting and surfaces it in Go
// tooling.
type Initializer interface {
	OnInit(ctx *Ctx) error
}

// Connector is the optional lifecycle hook that fires once when the
// SSE stream first opens for this tab. Bots that hit GET without ever
// opening the SSE never see this fire, so expensive background work
// (Stream tickers, fan-out goroutines) belongs here rather than in
// OnInit. A non-nil error is logged.
type Connector interface {
	OnConnect(ctx *Ctx) error
}

// Disposer is the optional lifecycle hook that fires when the tab's
// Ctx is torn down — page unload, ctx-TTL sweep, or app shutdown.
// Release resources, close goroutines, persist final state. Runs
// under the per-Ctx action mutex so it observes a composition that
// isn't being mutated by a concurrent handler.
type Disposer interface {
	OnDispose(ctx *Ctx)
}

// Mountable is the target of [Mount]. Implemented by *App (mounts at
// route on the app) and *Group (mounts under the group's prefix with
// the group's middleware applied to page render, action POST, and SSE
// handshake). The interface has only unexported methods so external
// types cannot implement it.
type Mountable interface {
	mountDescriptor(d *cmpDescriptor, route string)
}

// Mount registers a typed composition C at route on target.
//
// target may be an *App (route is taken as-is) or a *Group (route is
// joined under the group's prefix; the group's middleware chain wraps
// the rendered route + action POST + SSE handshake).
//
//	via.Mount[Counter](app, "/counter")
//
//	api := app.Group("/api")
//	api.Use(requireAuth)
//	via.Mount[Profile](api, "/profile")
//
// C must be a struct whose pointer type satisfies the Composition
// interface (i.e. has a View(ctx *Ctx) h.H method). Reflection runs
// once at Mount time to:
//
//   - validate View, OnInit, OnConnect, OnDispose signatures (panics with
//     a format-the-fix-yourself message on a mismatch);
//   - collect Signal[T] / StateTab[T] / StateSess[T] / StateApp[T]
//     fields and assign their wire keys (lowercased field name, or
//     `via:"name"` tag override);
//   - collect path:"name" / query:"name" tagged fields;
//   - enumerate exported methods of signature func(*Ctx) error or
//     func(*Ctx) and register them as actions.
//
// Per-request handlers do no reflection on the hot path for already-
// bound state. Mount panics if the route conflicts with an earlier
// registration on the same App.
func Mount[C any](target Mountable, route string) {
	target.mountDescriptor(buildDescriptor[C](), route)
}

func buildDescriptor[C any]() *cmpDescriptor {
	var zero C
	typ := reflect.TypeOf(zero)
	if typ == nil {
		// C is an interface (zero value is nil interface) — reflect.TypeOf
		// on a zero-interface returns nil. Use reflect.TypeOf(new(C)).Elem()
		// to recover the interface's type name for the error message.
		ifaceTyp := reflect.TypeOf(new(C)).Elem()
		panic("via.Mount: C must be a concrete struct, got interface type " +
			ifaceTyp.String())
	}
	if typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		panic("via.Mount: C must be a struct, got " + typ.String() + " (kind: " + typ.Kind().String() + ")")
	}

	descriptorMu.RLock()
	if d, ok := descriptorCache[typ]; ok {
		descriptorMu.RUnlock()
		clone := *d
		return &clone
	}
	descriptorMu.RUnlock()

	ptrTyp := reflect.PointerTo(typ)
	viewMethod, ok := ptrTyp.MethodByName("View")
	if !ok {
		panic(fmt.Sprintf(
			"via.Mount(%s): missing required method\n"+
				"\n"+
				"  func (c *%s) View(ctx *via.CtxR) h.H { ... }\n",
			typ.String(), typ.Name()))
	}
	checkViewSignature(typ, viewMethod)
	initIdx := checkAndIndexLifecycle(typ, ptrTyp, "OnInit", sigErrReturn)
	connectIdx := checkAndIndexLifecycle(typ, ptrTyp, "OnConnect", sigErrReturn)
	disposeIdx := checkAndIndexLifecycle(typ, ptrTyp, "OnDispose", sigVoid)

	desc := &cmpDescriptor{
		typ:          typ,
		actionByName: map[string]int{},
		viewIdx:      viewMethod.Index,
		initIdx:      -1,
		connectIdx:   -1,
		disposeIdx:   -1,
		bind:         &bindGuard{},
	}

	walkStruct(desc, typ, nil, "")

	// Signal and scope (StateSess/StateApp) handles all mirror into the same
	// data-signals namespace; two fields resolving to one wire key would
	// silently clobber each other in the initial-signals map. That's a
	// registration mistake — fail loud at Mount.
	seenKeys := make(map[string]struct{}, len(desc.signalSlots)+len(desc.scopeSlots))
	checkWireKey := func(key string) {
		if _, dup := seenKeys[key]; dup {
			panic(fmt.Sprintf(
				"via.Mount(%s): duplicate signal wire key %q — two fields resolve to the same key; rename one or set a distinct `via:\"name\"` tag",
				typ, key))
		}
		seenKeys[key] = struct{}{}
	}
	for _, s := range desc.signalSlots {
		checkWireKey(s.wireKey)
	}
	for _, s := range desc.scopeSlots {
		checkWireKey(s.wireKey)
	}

	for i := range ptrTyp.NumMethod() {
		m := ptrTyp.Method(i)
		void, ok := actionMethodKind(m)
		if !ok {
			continue
		}
		idx := len(desc.actionSlots)
		desc.actionSlots = append(desc.actionSlots, actionSlot{
			name:        m.Name,
			methodIndex: i,
			voidReturn:  void,
		})
		desc.actionByName[m.Name] = idx
	}

	desc.initIdx = initIdx
	desc.connectIdx = connectIdx
	desc.disposeIdx = disposeIdx

	descriptorMu.Lock()
	descriptorCache[typ] = desc
	descriptorMu.Unlock()
	// Return a shallow clone so the per-mount route + groupMW writes
	// don't race with concurrent buildDescriptor reads on the cached
	// entry. Invariant: every other descriptor field is treated as
	// read-only after this point — slot slices (signalSlots /
	// actionSlots / scopeSlots / paramSlots / querySlots / fileSlots)
	// are shared across clones of the same C, and mutating them on
	// one mount would silently corrupt every other mount. Only
	// per-mount fields (route, groupMW) may be assigned post-clone.
	clone := *desc
	return &clone
}

func checkPathParams(d *cmpDescriptor, route string) {
	declared := map[string]bool{}
	for seg := range strings.SplitSeq(route, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			declared[strings.Trim(seg, "{}")] = true
		}
	}
	for _, p := range d.paramSlots {
		if !declared[p.name] {
			panic(fmt.Sprintf(
				"via.Mount(%s): path:%q has no matching {%s} in route %q",
				d.typ.Name(), p.name, p.name, route,
			))
		}
	}
}
