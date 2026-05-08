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
	fieldIndex int
	kind       signalKind
	localID    string
	wireKey    string
	scalarKind reflect.Kind
	initRaw    string
}

type paramSlot struct {
	fieldIndex int
	name       string
	kind       reflect.Kind
}

type actionSlot struct {
	name        string
	methodIndex int
	method      reflect.Method
}

type cmpDescriptor struct {
	typ          reflect.Type
	ptrTyp       reflect.Type
	route        string
	signalSlots  []signalSlot
	paramSlots   []paramSlot
	actionSlots  []actionSlot
	actionByName map[string]int
	hasInit      bool
	hasDispose   bool
	app          *App
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

// MountOn mounts a composition C at a path under the group prefix.
func MountOn[C any](g *Group, route string) {
	full := joinPath(g.prefix, route)
	desc := buildDescriptor[C](g.app, full)
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
	if _, ok := ptrTyp.MethodByName("View"); !ok {
		panic(fmt.Sprintf("via.Mount: %s must implement View(ctx *Ctx) h.H", typ.String()))
	}

	desc := &cmpDescriptor{
		typ:          typ,
		ptrTyp:       ptrTyp,
		route:        route,
		actionByName: map[string]int{},
		app:          app,
	}

	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		switch role := classifyField(f); role {
		case roleSignal, roleState:
			s := signalSlot{
				fieldIndex: i,
				kind:       kindFor(role),
				localID:    parseLocalID(f),
				initRaw:    parseInitTag(f),
				scalarKind: peekValueKind(f.Type),
			}
			s.wireKey = s.localID
			desc.signalSlots = append(desc.signalSlots, s)
		case roleParam:
			desc.paramSlots = append(desc.paramSlots, paramSlot{
				fieldIndex: i,
				name:       f.Tag.Get("path"),
				kind:       f.Type.Kind(),
			})
		}
	}

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

	if _, ok := ptrTyp.MethodByName("Init"); ok {
		desc.hasInit = true
	}
	if _, ok := ptrTyp.MethodByName("Dispose"); ok {
		desc.hasDispose = true
	}

	checkPathParams(desc, route)

	descriptorMu.Lock()
	descriptorCache[typ] = desc
	descriptorMu.Unlock()
	return desc
}

type fieldRole int

const (
	roleNone fieldRole = iota
	roleSignal
	roleState
	roleParam
)

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
	return roleNone
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
	case "View", "Init", "Dispose":
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
