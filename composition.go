package via

import (
	"fmt"
	"net/http"
	"reflect"

	"github.com/go-via/via/h"
)

type Composition interface {
	View(ctx *Ctx) h.H
}

type cmpDescriptor struct {
	typ       reflect.Type
	route     string
	viewMethod reflect.Method
}

func Mount[C any](app *App, route string) {
	typ := reflect.TypeOf((*C)(nil))
	if typ.Kind() != reflect.Ptr {
		typ = reflect.New(typ).Type()
	} else {
		typ = typ.Elem()
	}

	ptrTyp := reflect.New(typ).Type()
	viewMethod, hasView := ptrTyp.MethodByName("View")
	if !hasView {
		panic(fmt.Sprintf("via: %s must implement View(ctx *Ctx) h.H", typ.String()))
	}

	desc := &cmpDescriptor{
		typ:       typ,
		route:     route,
		viewMethod: viewMethod,
	}

	app.mux.HandleFunc("GET "+route, func(w http.ResponseWriter, r *http.Request) {
		cmpVal := reflect.New(typ)

		ctx := &Ctx{
			id: "via_" + route,
		}

		args := []reflect.Value{cmpVal, reflect.ValueOf(ctx)}
		ret := desc.viewMethod.Func.Call(args)
		if len(ret) > 0 && !ret[0].IsNil() {
			view := ret[0].Interface().(h.H)
			_ = view.Render(w)
		}
	})
}
