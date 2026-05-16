// Package via builds reactive web UIs from typed Go structs.
//
// A composition is a struct. Its fields declare reactive state (Signal[T],
// State[T]) and path parameters (path:"name" tag). Its methods of signature
// func(*Ctx) error become server actions. View(*Ctx) h.H draws it.
//
//	type Counter struct {
//	    Hits via.State[int]
//	    Step via.Signal[int] `via:"step,init=1"`
//	}
//	func (c *Counter) Inc(ctx *via.Ctx) error {
//	    via.Add(ctx, &c.Hits, c.Step.Get(ctx))
//	    return nil
//	}
//	func (c *Counter) View(ctx *via.Ctx) h.H { ... }
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

// Composition is anything that renders a view from a Ctx. Types whose pointer
// satisfies this interface are mountable.
type Composition interface {
	View(ctx *Ctx) h.H
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
//   - collect Signal[T] / State[T] / scope.User[T] / scope.App[T]
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
		panic("via.Mount: C must be a concrete struct type")
	}
	if typ.Kind() == reflect.Ptr {
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
				"  func (c *%s) View(ctx *via.Ctx) h.H { ... }\n",
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
	}

	walkStruct(desc, typ, nil, "")

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
	// Return a clone so the per-mount route + groupMW writes don't race
	// with concurrent buildDescriptor reads on the cached entry.
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
