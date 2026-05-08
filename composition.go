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
//	    c.Hits.Set(ctx, c.Hits.Get(ctx)+c.Step.Get(ctx))
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
	"strconv"
	"strings"
	"sync"

	"github.com/go-via/via/h"
)

// Composition is anything that renders a view from a Ctx. Types whose pointer
// satisfies this interface are mountable.
type Composition interface {
	View(ctx *Ctx) h.H
}

// signalKind identifies the field's storage flavor for the descriptor walk.
type signalKind int

const (
	kindSignal signalKind = iota
	kindState
)

type signalSlot struct {
	fieldPath  []int // index path from root *C
	kind       signalKind
	localID    string
	wireKey    string
	scalarKind reflect.Kind
	initRaw    string
}

type scopeKind int

const (
	scopeUser scopeKind = iota
	scopeApp
)

type scopeSlot struct {
	fieldPath []int // index path from root *C
	kind      scopeKind
	wireKey   string // session/app store key
}

type paramSlot struct {
	fieldPath []int
	name      string
	kind      reflect.Kind
}

type actionSlot struct {
	name        string
	methodIndex int
	method      reflect.Method
}

type childSlot struct {
	fieldIndex int
	pathPrefix string // qualified prefix for nested signals/state, e.g. "Chart"
}

type cmpDescriptor struct {
	typ          reflect.Type
	ptrTyp       reflect.Type
	route        string
	signalSlots  []signalSlot
	scopeSlots   []scopeSlot
	paramSlots   []paramSlot
	actionSlots  []actionSlot
	actionByName map[string]int
	childSlots   []childSlot
	viewIdx      int // method index of View on *C
	initIdx      int // method index of Init or -1
	connectIdx   int // method index of OnConnect or -1
	disposeIdx   int // method index of Dispose or -1
	hasInit      bool
	hasOnConnect bool
	hasDispose   bool
	app          *App

	groupMW []Middleware // middleware from the owning Group, if any
}

var (
	descriptorMu    sync.RWMutex
	descriptorCache = map[reflect.Type]*cmpDescriptor{}
)

// Mount registers a typed composition C at the given route.
func Mount[C any](app *App, route string) {
	desc := buildDescriptor[C](app, route)
	app.registerDescriptor(desc)
}

// MountOn mounts a composition C at a path under the group prefix. The
// group's middleware chain wraps the rendered route.
func MountOn[C any](g *Group, route string) {
	full := joinPath(g.prefix, route)
	desc := buildDescriptor[C](g.app, full)
	desc.groupMW = append([]Middleware(nil), g.middleware...)
	g.app.registerDescriptor(desc)
}

func buildDescriptor[C any](app *App, route string) *cmpDescriptor {
	var zero C
	typ := reflect.TypeOf(zero)
	if typ == nil {
		panic("via.Mount: C must be a concrete struct type")
	}
	if typ.Kind() == reflect.Ptr {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct {
		panic("via.Mount: C must be a struct, got " + typ.Kind().String())
	}

	descriptorMu.RLock()
	if d, ok := descriptorCache[typ]; ok {
		descriptorMu.RUnlock()
		clone := *d
		clone.route = route
		clone.app = app
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
	checkLifecycleSignature(typ, ptrTyp, "Init", initSig)
	checkLifecycleSignature(typ, ptrTyp, "OnConnect", initSig)
	checkLifecycleSignature(typ, ptrTyp, "Dispose", disposeSig)

	desc := &cmpDescriptor{
		typ:          typ,
		ptrTyp:       ptrTyp,
		route:        route,
		actionByName: map[string]int{},
		viewIdx:      viewMethod.Index,
		initIdx:      -1,
		connectIdx:   -1,
		disposeIdx:   -1,
		app:          app,
	}

	walkStruct(desc, typ, nil, "")

	for i := 0; i < ptrTyp.NumMethod(); i++ {
		m := ptrTyp.Method(i)
		if !isActionMethod(m) {
			continue
		}
		idx := len(desc.actionSlots)
		desc.actionSlots = append(desc.actionSlots, actionSlot{
			name:        m.Name,
			methodIndex: i,
			method:      m,
		})
		desc.actionByName[m.Name] = idx
	}

	if m, ok := ptrTyp.MethodByName("Init"); ok {
		desc.hasInit = true
		desc.initIdx = m.Index
	}
	if m, ok := ptrTyp.MethodByName("OnConnect"); ok {
		desc.hasOnConnect = true
		desc.connectIdx = m.Index
	}
	if m, ok := ptrTyp.MethodByName("Dispose"); ok {
		desc.hasDispose = true
		desc.disposeIdx = m.Index
	}

	checkPathParams(desc, route)

	descriptorMu.Lock()
	descriptorCache[typ] = desc
	descriptorMu.Unlock()
	return desc
}

// expected method signatures for lifecycle hooks; Mount validates against
// these and panics with a helpful, format-the-fix-yourself message if a
// method exists but has the wrong shape.
type lifecycleSig struct {
	in     int    // number of inputs (incl. receiver)
	out    int    // number of outputs
	ctxIn  bool   // true if input[1] must be *Ctx
	errOut bool   // true if output[0] must be error
	repr   string // human-readable form of the expected signature
}

var (
	initSig = lifecycleSig{in: 2, out: 1, ctxIn: true, errOut: true,
		repr: "func (c *T) %s(ctx *via.Ctx) error"}
	disposeSig = lifecycleSig{in: 2, out: 0, ctxIn: true, errOut: false,
		repr: "func (c *T) %s(ctx *via.Ctx)"}
)

func checkLifecycleSignature(typ, ptrTyp reflect.Type, name string, want lifecycleSig) {
	m, ok := ptrTyp.MethodByName(name)
	if !ok {
		return
	}
	mt := m.Type
	bad := false
	if mt.NumIn() != want.in || mt.NumOut() != want.out {
		bad = true
	}
	if !bad && want.ctxIn && mt.In(1) != reflect.TypeOf((*Ctx)(nil)) {
		bad = true
	}
	if !bad && want.errOut && mt.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
		bad = true
	}
	if bad {
		panic(fmt.Sprintf(
			"via.Mount(%s): %s has the wrong signature\n"+
				"\n"+
				"  expected: "+want.repr+"\n",
			typ.String(), name, name))
	}
}

func checkViewSignature(typ reflect.Type, m reflect.Method) {
	mt := m.Type
	if mt.NumIn() != 2 || mt.NumOut() != 1 ||
		mt.In(1) != reflect.TypeOf((*Ctx)(nil)) {
		panic(fmt.Sprintf(
			"via.Mount(%s): View has the wrong signature\n"+
				"\n"+
				"  expected: func (c *%s) View(ctx *via.Ctx) h.H\n",
			typ.String(), typ.Name()))
	}
	// View's return type is checked structurally — h.H is an interface so
	// the concrete return type can be anything assignable to it; we trust
	// the assertion at render time.
}

type fieldRole int

const (
	roleNone fieldRole = iota
	roleSignal
	roleState
	roleScopeUser
	roleScopeApp
	roleParam
	roleChild
)

// walkStruct recursively flattens a composition tree into the descriptor.
// pathPrefix is the qualified wire key prefix for nested children
// (empty at root, "Chart" at one level, "Tab.Chart" at two).
// indexPath is the slice of struct-field indices from the root *C to the
// struct being walked (so the runtime can address nested fields via
// reflect.Value.FieldByIndex).
func walkStruct(d *cmpDescriptor, typ reflect.Type, indexPath []int, pathPrefix string) {
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		fieldPath := append(append([]int{}, indexPath...), i)
		switch role := classifyField(f); role {
		case roleSignal, roleState:
			local := parseLocalID(f)
			wire := local
			if pathPrefix != "" {
				wire = pathPrefix + "." + local
			}
			d.signalSlots = append(d.signalSlots, signalSlot{
				fieldPath:  fieldPath,
				kind:       kindFor(role),
				localID:    local,
				wireKey:    wire,
				initRaw:    parseInitTag(f),
				scalarKind: peekValueKind(f.Type),
			})
		case roleScopeUser, roleScopeApp:
			local := parseLocalID(f)
			wire := local
			if pathPrefix != "" {
				wire = pathPrefix + "." + local
			}
			kind := scopeUser
			if role == roleScopeApp {
				kind = scopeApp
			}
			d.scopeSlots = append(d.scopeSlots, scopeSlot{
				fieldPath: fieldPath,
				kind:      kind,
				wireKey:   wire,
			})
		case roleParam:
			d.paramSlots = append(d.paramSlots, paramSlot{
				fieldPath: fieldPath,
				name:      f.Tag.Get("path"),
				kind:      f.Type.Kind(),
			})
		case roleChild:
			child := f.Type
			if child.Kind() == reflect.Ptr {
				child = child.Elem()
			}
			next := f.Name
			if pathPrefix != "" {
				next = pathPrefix + "." + f.Name
			}
			d.childSlots = append(d.childSlots, childSlot{
				fieldIndex: i, pathPrefix: next,
			})
			walkStruct(d, child, fieldPath, next)
		}
	}
}

func classifyField(f reflect.StructField) fieldRole {
	if _, ok := f.Tag.Lookup("path"); ok {
		return roleParam
	}
	if isSignalType(f.Type) {
		return roleSignal
	}
	if isStateType(f.Type) {
		return roleState
	}
	if isScopeUserType(f.Type) {
		return roleScopeUser
	}
	if isScopeAppType(f.Type) {
		return roleScopeApp
	}
	if isChildComposition(f.Type) {
		return roleChild
	}
	return roleNone
}

func isScopeUserType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return strings.HasPrefix(t.Name(), "User[") &&
		t.PkgPath() == "github.com/go-via/via/scope"
}

func isScopeAppType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return strings.HasPrefix(t.Name(), "App[") &&
		t.PkgPath() == "github.com/go-via/via/scope"
}

// isChildComposition reports whether t is a struct (or pointer-to-struct)
// in a third-party package whose pointer type implements via.Composition.
// Path-tag and Signal/State handles are special-cased earlier and do not
// recurse here. We exclude types whose package matches our own to avoid
// recursing into Signal[T]/State[T]'s internal struct.
func isChildComposition(t reflect.Type) bool {
	tt := t
	if tt.Kind() == reflect.Ptr {
		tt = tt.Elem()
	}
	if tt.Kind() != reflect.Struct {
		return false
	}
	// our own handle types (Signal[T], State[T]) live in the via package;
	// skip them so we don't recurse into private fields.
	if tt.PkgPath() == "github.com/go-via/via" {
		return false
	}
	ptr := reflect.PointerTo(tt)
	_, hasView := ptr.MethodByName("View")
	return hasView
}

// isSignalType returns true if t is a Signal[T] for some T. We detect by
// the type name prefix to avoid relying on a sentinel field.
func isSignalType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	name := t.Name()
	return strings.HasPrefix(name, "Signal[") && t.PkgPath() == "github.com/go-via/via"
}

func isStateType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	name := t.Name()
	return strings.HasPrefix(name, "State[") && t.PkgPath() == "github.com/go-via/via"
}

func kindFor(r fieldRole) signalKind {
	if r == roleSignal {
		return kindSignal
	}
	return kindState
}

func parseLocalID(f reflect.StructField) string {
	if tag := f.Tag.Get("via"); tag != "" {
		parts := strings.Split(tag, ",")
		if parts[0] != "" {
			return parts[0]
		}
	}
	return strings.ToLower(f.Name[:1]) + f.Name[1:]
}

func parseInitTag(f reflect.StructField) string {
	tag := f.Tag.Get("via")
	if tag == "" {
		return ""
	}
	for _, part := range strings.Split(tag, ",") {
		if strings.HasPrefix(part, "init=") {
			return strings.TrimPrefix(part, "init=")
		}
	}
	return ""
}

// peekValueKind returns the Kind of the inner T for Signal[T]/State[T]. The
// handle structs put the value as their first field.
func peekValueKind(t reflect.Type) reflect.Kind {
	if t.Kind() != reflect.Struct || t.NumField() == 0 {
		return reflect.Invalid
	}
	return t.Field(0).Type.Kind()
}

func isActionMethod(m reflect.Method) bool {
	mt := m.Type
	if mt.NumIn() != 2 || mt.NumOut() != 1 {
		return false
	}
	if mt.In(1) != reflect.TypeOf((*Ctx)(nil)) {
		return false
	}
	if mt.Out(0) != reflect.TypeOf((*error)(nil)).Elem() {
		return false
	}
	switch m.Name {
	case "View", "Init", "OnConnect", "Dispose":
		return false
	}
	return true
}

func checkPathParams(d *cmpDescriptor, route string) {
	declared := map[string]bool{}
	for _, seg := range strings.Split(route, "/") {
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

func decodeParam(v reflect.Value, kind reflect.Kind, raw string) {
	switch kind {
	case reflect.String:
		v.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, _ := strconv.ParseInt(raw, 10, 64)
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, _ := strconv.ParseUint(raw, 10, 64)
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, _ := strconv.ParseFloat(raw, 64)
		v.SetFloat(f)
	case reflect.Bool:
		b, _ := strconv.ParseBool(raw)
		v.SetBool(b)
	}
}
