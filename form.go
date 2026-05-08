package via

import (
	"net/url"
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
	getValue := func(key string) (string, bool) {
		if signals != nil {
			if v, ok := signals[key]; ok {
				return formatScalar(v), true
			}
		}
		if r != nil {
			if v := r.URL.Query().Get(key); v != "" {
				return v, true
			}
			if r.PostForm == nil {
				_ = r.ParseForm()
			}
			if v := r.PostFormValue(key); v != "" {
				return v, true
			}
		}
		return "", false
	}

	for i := 0; i < rt.NumField(); i++ {
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
		decodeFormField(rv.Field(i), f.Type.Kind(), raw)
	}
	return nil
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return string(toLower(s[0])) + s[1:]
}

func toLower(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
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

func decodeFormField(v reflect.Value, kind reflect.Kind, raw string) {
	switch kind {
	case reflect.String:
		v.SetString(raw)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil {
			v.SetInt(n)
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		if n, err := strconv.ParseUint(raw, 10, 64); err == nil {
			v.SetUint(n)
		}
	case reflect.Float32, reflect.Float64:
		if f, err := strconv.ParseFloat(raw, 64); err == nil {
			v.SetFloat(f)
		}
	case reflect.Bool:
		if b, err := strconv.ParseBool(raw); err == nil {
			v.SetBool(b)
		}
	}
	_ = url.Values{} // keep url imported for future multi-form handling
}
