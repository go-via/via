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
	fieldPath []int // index path from root *C
	kind      signalKind
	wireKey   string
	initRaw   string
}

// scopeBinder is implemented by scope.User[T] / scope.App[T] (pointer
// receiver) so the runtime can write the wire key into the handle's
// unexported storage without going through reflect.FieldByName.
type scopeBinder interface{ BindWireKey(string) }

type scopeSlot struct {
	fieldPath []int  // index path from root *C
	wireKey   string // session/app store key
}

// kindedSlot is the shared shape for path:"…" and query:"…" tagged
// fields. They differ only in source (r.PathValue vs r.URL.Query); the
// slot data itself is identical.
type kindedSlot struct {
	fieldPath []int
	name      string
	kind      reflect.Kind
}

type actionSlot struct {
	name        string
	methodIndex int
	voidReturn  bool // true if the method has signature func(*Ctx) (no error)
}

type cmpDescriptor struct {
	typ          reflect.Type
	route        string
	signalSlots  []signalSlot
	scopeSlots   []scopeSlot
	paramSlots   []kindedSlot
	querySlots   []kindedSlot
	fileSlots    []fileSlot
	actionSlots  []actionSlot
	actionByName map[string]int
	viewIdx      int // method index of View on *C
	initIdx      int // method index of OnInit or -1
	connectIdx   int // method index of OnConnect or -1
	disposeIdx   int // method index of OnDispose or -1

	groupMW []Middleware // middleware from the owning Group, if any
}

var (
	descriptorMu    sync.RWMutex
	descriptorCache = map[reflect.Type]*cmpDescriptor{}
)

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

// expected method signatures for lifecycle hooks; Mount validates against
// these and panics with a helpful, format-the-fix-yourself message if a
// method exists but has the wrong shape.
//
// All lifecycle methods take exactly one *Ctx argument; only the return
// shape varies (error vs no return), so we only need to encode that.
type lifecycleSig struct {
	out    int    // number of outputs (0 for no-return, 1 for error)
	errOut bool   // true if output[0] must be error
	repr   string // human-readable form of the expected signature
}

var (
	sigErrReturn = lifecycleSig{out: 1, errOut: true, repr: "func (c *T) %s(ctx *via.Ctx) error"}
	sigVoid      = lifecycleSig{out: 0, errOut: false, repr: "func (c *T) %s(ctx *via.Ctx)"}

	// Cached reflect.Type values used by Mount-time signature checks.
	// reflect.TypeOf returns the same canonical type per call but each call
	// still allocates an interface header — cache once at package init.
	ctxPtrType = reflect.TypeOf((*Ctx)(nil))
	errorType  = reflect.TypeOf((*error)(nil)).Elem()
)

// checkAndIndexLifecycle validates the lifecycle method's signature and
// returns its method index, or -1 if the method doesn't exist on ptrTyp.
// Combines the signature check and the index lookup so callers don't
// have to call ptrTyp.MethodByName twice.
func checkAndIndexLifecycle(typ, ptrTyp reflect.Type, name string, want lifecycleSig) int {
	m, ok := ptrTyp.MethodByName(name)
	if !ok {
		return -1
	}
	mt := m.Type
	// && short-circuits: when NumOut != want.out (especially 0 vs 1)
	// the Out(0) call is skipped, so this is safe for the void case.
	bad := mt.NumIn() != 2 || mt.In(1) != ctxPtrType ||
		mt.NumOut() != want.out ||
		(want.errOut && mt.Out(0) != errorType)
	if bad {
		panic(fmt.Sprintf(
			"via.Mount(%s): %s has the wrong signature\n"+
				"\n"+
				"  expected: "+want.repr+"\n",
			typ.String(), name, name))
	}
	return m.Index
}

func checkViewSignature(typ reflect.Type, m reflect.Method) {
	mt := m.Type
	if mt.NumIn() != 2 || mt.NumOut() != 1 ||
		mt.In(1) != ctxPtrType {
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
	roleQuery
	roleFile
	roleChild
)

// walkStruct recursively flattens a composition tree into the descriptor.
// pathPrefix is the qualified wire key prefix for nested children
// (empty at root, "Chart" at one level, "Tab.Chart" at two).
// indexPath is the slice of struct-field indices from the root *C to the
// struct being walked (so the runtime can address nested fields via
// reflect.Value.FieldByIndex).
func walkStruct(d *cmpDescriptor, typ reflect.Type, indexPath []int, pathPrefix string) {
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		// Allocate exactly once (len+1) instead of the double-append idiom,
		// which can over-allocate via Go's slice growth heuristics.
		fieldPath := make([]int, len(indexPath)+1)
		copy(fieldPath, indexPath)
		fieldPath[len(indexPath)] = i
		switch role := classifyField(f); role {
		case roleSignal, roleState:
			kind := kindSignal
			if role == roleState {
				kind = kindState
			}
			d.signalSlots = append(d.signalSlots, signalSlot{
				fieldPath: fieldPath,
				kind:      kind,
				wireKey:   qualify(pathPrefix, parseLocalID(f)),
				initRaw:   parseInitTag(f),
			})
		case roleScopeUser, roleScopeApp:
			handleType := f.Type
			if handleType.Kind() == reflect.Ptr {
				handleType = handleType.Elem()
			}
			if !reflect.PointerTo(handleType).Implements(reflect.TypeOf((*scopeBinder)(nil)).Elem()) {
				panic("via.Mount: scope handle " + handleType.String() +
					" must implement BindWireKey(string)")
			}
			d.scopeSlots = append(d.scopeSlots, scopeSlot{
				fieldPath: fieldPath,
				wireKey:   qualify(pathPrefix, parseLocalID(f)),
			})
		case roleParam:
			d.paramSlots = append(d.paramSlots, kindedSlot{
				fieldPath: fieldPath,
				name:      f.Tag.Get("path"),
				kind:      f.Type.Kind(),
			})
		case roleQuery:
			d.querySlots = append(d.querySlots, kindedSlot{
				fieldPath: fieldPath,
				name:      f.Tag.Get("query"),
				kind:      f.Type.Kind(),
			})
		case roleFile:
			d.fileSlots = append(d.fileSlots, fileSlot{
				fieldPath: fieldPath,
				wireKey:   qualify(pathPrefix, parseLocalID(f)),
			})
		case roleChild:
			child := f.Type
			if child.Kind() == reflect.Ptr {
				child = child.Elem()
			}
			walkStruct(d, child, fieldPath, qualify(pathPrefix, f.Name))
		}
	}
}

func classifyField(f reflect.StructField) fieldRole {
	if _, ok := f.Tag.Lookup("path"); ok {
		return roleParam
	}
	if _, ok := f.Tag.Lookup("query"); ok {
		return roleQuery
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
	if isFileType(f.Type) {
		return roleFile
	}
	if isChildComposition(f.Type) {
		return roleChild
	}
	return roleNone
}

// Package paths used to identify our own handle types via reflection.
// Stored as constants so the four classifyField helpers below all
// reference the same canonical strings.
const (
	viaPkgPath   = "github.com/go-via/via"
	scopePkgPath = "github.com/go-via/via/scope"
)

func isScopeUserType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return strings.HasPrefix(t.Name(), "User[") && t.PkgPath() == scopePkgPath
}

func isScopeAppType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return strings.HasPrefix(t.Name(), "App[") && t.PkgPath() == scopePkgPath
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
	if tt.PkgPath() == viaPkgPath {
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
	return strings.HasPrefix(t.Name(), "Signal[") && t.PkgPath() == viaPkgPath
}

func isStateType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return strings.HasPrefix(t.Name(), "State[") && t.PkgPath() == viaPkgPath
}

// qualify joins a dotted path prefix and a name into a wire key. Returns
// the bare name when the prefix is empty so the top-level composition's
// signals stay one level deep.
func qualify(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func parseLocalID(f reflect.StructField) string {
	if tag := f.Tag.Get("via"); tag != "" {
		// Only the first comma-separated segment is the wire key — the
		// rest is options like `init=…`. strings.Cut is one linear scan,
		// no slice allocation.
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			return name
		}
	}
	return lowerFirst(f.Name)
}

func parseInitTag(f reflect.StructField) string {
	tag := f.Tag.Get("via")
	if tag == "" {
		return ""
	}
	// SplitSeq (Go 1.24+) avoids the []string allocation strings.Split
	// would make to hold every comma-separated segment.
	for part := range strings.SplitSeq(tag, ",") {
		if v, ok := strings.CutPrefix(part, "init="); ok {
			return v
		}
	}
	return ""
}

// actionMethodKind reports whether m is a valid action method and its
// return shape. Recognised signatures:
//
//	func (c *T) Inc(ctx *via.Ctx) error  // void=false
//	func (c *T) Inc(ctx *via.Ctx)        // void=true (no return)
//
// Lifecycle method names are excluded so they don't masquerade as
// actions when their signature happens to match.
func actionMethodKind(m reflect.Method) (void bool, ok bool) {
	mt := m.Type
	if mt.NumIn() != 2 {
		return false, false
	}
	if mt.In(1) != ctxPtrType {
		return false, false
	}
	switch m.Name {
	case "View", "OnInit", "OnConnect", "OnDispose":
		return false, false
	}
	switch mt.NumOut() {
	case 0:
		return true, true
	case 1:
		if mt.Out(0) != errorType {
			return false, false
		}
		return false, true
	}
	return false, false
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
