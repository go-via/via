package via

import (
	"fmt"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/go-via/via/h"
)

// Composition is the shape every mountable and nestable type must satisfy.
// A "page" is a Composition mounted at a route; a "component" is a
// Composition nested inside another Composition's view.
type Composition interface {
	View(ctx *Ctx) h.H
}

var compositionIface = reflect.TypeOf((*Composition)(nil)).Elem()

// cmpDescriptor caches the one-time reflection walk of a mounted composition.
type cmpDescriptor struct {
	typ        reflect.Type
	route      string
	paramSlots []paramSlot
	cmpPool    sync.Pool
}

type paramSlot struct {
	name       string
	fieldIndex int
	kind       reflect.Kind
}

// Mount registers C as a composition served at route. Panics at registration
// if *C does not satisfy Composition, if route parameters conflict with the
// struct layout, or if the route is otherwise ill-formed.
func Mount[C any](app *App, route string) {
	typ := compositionTypeOf[C]()
	app.mountComposition(typ, route, nil)
}

// MountOn is the Group-scoped variant of Mount. The composition inherits the
// group's prefix, middleware, and (in future) layout.
func MountOn[C any](g *Group, route string) {
	typ := compositionTypeOf[C]()
	g.app.mountComposition(typ, joinPath(g.prefix, route), g.collectMiddleware())
}

func compositionTypeOf[C any]() reflect.Type {
	var zero C
	t := reflect.TypeOf(zero)
	if t == nil {
		panic("via.Mount: type parameter must be a concrete struct, not an interface")
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		panic(fmt.Sprintf("via.Mount: type parameter must be a struct, got %s", t.Kind()))
	}
	if !reflect.PointerTo(t).Implements(compositionIface) && !t.Implements(compositionIface) {
		panic(fmt.Sprintf("via.Mount: *%s does not implement via.Composition (missing View(ctx *via.Ctx) h.H)", t.Name()))
	}
	return t
}

func (a *App) mountComposition(typ reflect.Type, route string, groupMW []Middleware) {
	paramNames := extractParamNames(route)

	desc := &cmpDescriptor{
		typ:   typ,
		route: route,
	}
	desc.paramSlots = buildParamSlots(typ, route, paramNames)
	desc.cmpPool.New = func() any {
		return reflect.New(typ).Interface()
	}

	mwChain := append([]Middleware{}, a.middleware...)
	mwChain = append(mwChain, groupMW...)

	a.mux.HandleFunc("GET "+route, func(w http.ResponseWriter, r *http.Request) {
		if shouldSkipPageRequest(r.URL.Path) {
			http.NotFound(w, r)
			return
		}
		final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			desc.serve(a, w, r)
		})
		runMiddleware(mwChain, w, r, final)
	})
}

func buildParamSlots(typ reflect.Type, route string, paramNames []string) []paramSlot {
	paramSet := make(map[string]bool, len(paramNames))
	for _, n := range paramNames {
		paramSet[n] = true
	}
	var slots []paramSlot
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		name, ok := f.Tag.Lookup("path")
		if !ok {
			continue
		}
		if !paramSet[name] {
			panic(fmt.Sprintf("via.Mount: field %s.%s tagged path:%q has no matching {%s} in route %q",
				typ.Name(), f.Name, name, name, route))
		}
		if !isSupportedParamKind(f.Type.Kind()) {
			panic(fmt.Sprintf("via.Mount: field %s.%s tagged path:%q has unsupported type %s",
				typ.Name(), f.Name, name, f.Type))
		}
		slots = append(slots, paramSlot{name: name, fieldIndex: i, kind: f.Type.Kind()})
	}
	return slots
}

func isSupportedParamKind(k reflect.Kind) bool {
	switch k {
	case reflect.String,
		reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64,
		reflect.Float32, reflect.Float64,
		reflect.Bool:
		return true
	}
	return false
}

func shouldSkipPageRequest(p string) bool {
	return p == "/favicon.ico" ||
		strings.HasPrefix(p, "/.well-known/") ||
		strings.HasSuffix(p, ".js.map")
}

func (d *cmpDescriptor) serve(a *App, w http.ResponseWriter, r *http.Request) {
	cmpVal := d.cmpPool.Get()
	defer d.cmpPool.Put(cmpVal)

	if err := d.decodeParams(cmpVal, r); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	ctx := newMountCtx(a, r)
	cmp := cmpVal.(Composition)
	body := cmp.View(ctx)

	bodyEls := []h.H{body}
	bodyEls = append(bodyEls, a.documentFootIncludes...)

	doc := h.HTML5(h.HTML5Props{
		Title:     a.cfg.title,
		Head:      a.documentHeadIncludes,
		Body:      bodyEls,
		HTMLAttrs: a.documentHTMLAttrs,
	})
	_ = doc.Render(w)
}

func (d *cmpDescriptor) decodeParams(cmpVal any, r *http.Request) error {
	if len(d.paramSlots) == 0 {
		return nil
	}
	rv := reflect.ValueOf(cmpVal).Elem()
	for _, slot := range d.paramSlots {
		raw := r.PathValue(slot.name)
		fv := rv.Field(slot.fieldIndex)
		switch slot.kind {
		case reflect.String:
			fv.SetString(raw)
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			n, err := strconv.ParseInt(raw, 10, 64)
			if err != nil {
				return fmt.Errorf("path param %q: %v", slot.name, err)
			}
			fv.SetInt(n)
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			n, err := strconv.ParseUint(raw, 10, 64)
			if err != nil {
				return fmt.Errorf("path param %q: %v", slot.name, err)
			}
			fv.SetUint(n)
		case reflect.Float32, reflect.Float64:
			f, err := strconv.ParseFloat(raw, 64)
			if err != nil {
				return fmt.Errorf("path param %q: %v", slot.name, err)
			}
			fv.SetFloat(f)
		case reflect.Bool:
			b, err := strconv.ParseBool(raw)
			if err != nil {
				return fmt.Errorf("path param %q: %v", slot.name, err)
			}
			fv.SetBool(b)
		}
	}
	return nil
}

func newMountCtx(a *App, r *http.Request) *Ctx {
	ctx := &Ctx{
		id:       genRandID(),
		session:  sessionFromRequest(r),
		doneChan: make(chan struct{}),
	}
	ctx.touch()
	return ctx
}
