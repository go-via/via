package via

import (
	"reflect"
	"strconv"
)

// DecodeForm parses the request body's signal payload into a typed struct.
// Datastar action POSTs send signals as JSON; this helper lets actions
// pull a strongly-typed view of selected signals into a struct, decoded
// by `form:"name"` tags. Useful when the action wants a value-object
// instead of N separate Signal[T].Get(ctx) calls.
//
//	type LoginForm struct {
//	    Email    string `form:"email"`
//	    Password string `form:"password"`
//	}
//	func (p *Page) Submit(ctx *via.Ctx) error {
//	    var f LoginForm
//	    if err := via.DecodeForm(ctx, &f); err != nil {
//	        return err
//	    }
//	    ...
//	}
//
// Source resolution: the helper first checks the action's signal
// payload (read off r at dispatch and stored on ctx), then falls back
// to r.URL.Query() and r.PostForm if present. Tag-less fields use the
// lower-cased field name as the key.
func DecodeForm[T any](ctx *Ctx, dst *T) error {
	if ctx == nil || dst == nil {
		return nil
	}
	r := ctx.Request()
	rv := reflect.ValueOf(dst).Elem()
	rt := rv.Type()
	if rt.Kind() != reflect.Struct {
		return nil
	}

	signals := ctx.lastSignals
	// Hoist the query parse and PostForm population once per call —
	// r.URL.Query() reparses the raw query string on every invocation.
	var query map[string][]string
	if r != nil {
		query = r.URL.Query()
		if r.PostForm == nil {
			_ = r.ParseForm()
		}
	}
	getValue := func(key string) (string, bool) {
		if signals != nil {
			if v, ok := signals[key]; ok {
				return formatScalar(v), true
			}
		}
		if r != nil {
			if vs := query[key]; len(vs) > 0 && vs[0] != "" {
				return vs[0], true
			}
			if v := r.PostFormValue(key); v != "" {
				return v, true
			}
		}
		return "", false
	}

	for i := range rt.NumField() {
		f := rt.Field(i)
		if !f.IsExported() {
			continue
		}
		key := f.Tag.Get("form")
		if key == "" {
			key = lowerFirst(f.Name)
		}
		raw, ok := getValue(key)
		if !ok {
			continue
		}
		decodeScalarString(rv.Field(i), f.Type.Kind(), raw)
	}
	return nil
}

func formatScalar(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return ""
}
