package via

import (
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"strconv"
)

// Scalar encode / decode shared between Signal[T] and StateTab[T] (both
// implement signalRef and route through these helpers). Lives here
// rather than in signal.go because the logic is value-shape driven, not
// reactive-type driven — keeping it isolated makes the field-decoding
// hole audit (e.g. iter-8 bool/string init-tag fix) self-contained.

// jsonTrue / jsonFalse cache the only two possible Bool encodings so we
// don't reallocate the same 4 / 5 bytes on every render. The bytes are
// fed to json.RawMessage in writePageDocument which never mutates them.
var (
	jsonTrue  = []byte("true")
	jsonFalse = []byte("false")
)

// encodeScalar writes v as JSON without going through fmt.Sprintf.
// Falls back to encoding/json for composites (slices, maps, structs).
func encodeScalar(v reflect.Value) ([]byte, error) {
	switch v.Kind() {
	case reflect.String:
		return strconv.AppendQuote(nil, v.String()), nil
	case reflect.Bool:
		if v.Bool() {
			return jsonTrue, nil
		}
		return jsonFalse, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return strconv.AppendInt(nil, v.Int(), 10), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return strconv.AppendUint(nil, v.Uint(), 10), nil
	case reflect.Float32, reflect.Float64:
		// reflect.Value.Float widens a float32 to float64; formatting at
		// bitSize 64 would surface the widening (float32(0.1) → 0.10000000149011612).
		bits := 64
		if v.Kind() == reflect.Float32 {
			bits = 32
		}
		return strconv.AppendFloat(nil, v.Float(), 'g', -1, bits), nil
	}
	return json.Marshal(v.Interface())
}

// decodeScalarInto writes raw into dst, coercing across the JSON shapes
// (string, bool, float64) the action-payload decoder produces, plus the
// raw-string form struct tags arrive in. Unrecognised combinations
// leave dst at its zero value — best-effort decode is the contract
// (parse failures don't fail the request).
//
// Numeric truncation is silent: a float64 value that overflows the
// destination's narrower int/uint kind (e.g. 9999 into an int8) is
// truncated by the Set{Int,Uint,Float} reflect operation rather than
// clamped or rejected. Choose Signal[T]'s T to match the value range
// you accept from the client; validate explicitly inside the action
// handler if untrusted input might overflow.
func decodeScalarInto(dst reflect.Value, raw any) {
	// Best-effort: discard the shape/overflow error so server-side and
	// non-strict client decodes keep their lenient contract. WithStrictDecode
	// uses decodeScalarChecked directly to surface it.
	_ = decodeScalarChecked(dst, raw)
}

// decodeScalarChecked is decodeScalarInto with reporting: it performs the same
// best-effort Set (so a discarded error leaves behavior identical) but returns
// a non-nil error when raw's JSON shape doesn't match dst's kind or a numeric
// value overflows dst's width. The error never names the field — the caller
// (injectSignals) wraps it with the wire key.
func decodeScalarChecked(dst reflect.Value, raw any) error {
	if raw == nil {
		return nil
	}
	switch dst.Kind() {
	case reflect.String:
		s, ok := raw.(string)
		if !ok {
			return fmt.Errorf("expected string, got %T", raw)
		}
		dst.SetString(s)
	case reflect.Bool:
		switch v := raw.(type) {
		case bool:
			dst.SetBool(v)
		case string:
			// `via:"open,init=true"` arrives as a string from the struct
			// tag; ParseBool covers "true"/"false"/"1"/"0" and friends.
			b, err := strconv.ParseBool(v)
			if err != nil {
				return fmt.Errorf("cannot parse %q as bool", v)
			}
			dst.SetBool(b)
		default:
			return fmt.Errorf("expected bool, got %T", raw)
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		var val int64
		switch n := raw.(type) {
		case float64:
			if n != math.Trunc(n) {
				return fmt.Errorf("value %v is not an integer", n)
			}
			val = int64(n)
		case int64:
			val = n
		case int:
			val = int64(n)
		case string:
			i, err := strconv.ParseInt(n, 10, 64)
			if err != nil {
				return fmt.Errorf("cannot parse %q as integer", n)
			}
			val = i
		default:
			return fmt.Errorf("expected integer, got %T", raw)
		}
		if dst.OverflowInt(val) {
			dst.SetInt(val) // truncates — preserves best-effort for non-strict
			return fmt.Errorf("value %d overflows %s", val, dst.Kind())
		}
		dst.SetInt(val)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		var val uint64
		switch n := raw.(type) {
		case float64:
			if n != math.Trunc(n) {
				return fmt.Errorf("value %v is not an integer", n)
			}
			if n < 0 {
				dst.SetUint(uint64(int64(n)))
				return fmt.Errorf("value %v overflows unsigned %s", n, dst.Kind())
			}
			val = uint64(n)
		case uint64:
			val = n
		case string:
			i, err := strconv.ParseUint(n, 10, 64)
			if err != nil {
				return fmt.Errorf("cannot parse %q as unsigned integer", n)
			}
			val = i
		default:
			return fmt.Errorf("expected unsigned integer, got %T", raw)
		}
		if dst.OverflowUint(val) {
			dst.SetUint(val)
			return fmt.Errorf("value %d overflows %s", val, dst.Kind())
		}
		dst.SetUint(val)
	case reflect.Float32, reflect.Float64:
		var f float64
		switch n := raw.(type) {
		case float64:
			f = n
		case string:
			x, err := strconv.ParseFloat(n, 64)
			if err != nil {
				return fmt.Errorf("cannot parse %q as number", n)
			}
			f = x
		default:
			return fmt.Errorf("expected number, got %T", raw)
		}
		if dst.OverflowFloat(f) {
			dst.SetFloat(f)
			return fmt.Errorf("value %v overflows %s", f, dst.Kind())
		}
		dst.SetFloat(f)
		return nil
	case reflect.Slice, reflect.Map, reflect.Struct, reflect.Array:
		// Composite signals (SignalSlice/SignalMap) mirror the encodeScalar
		// json fallback: round-trip the already-decoded value back through
		// JSON into dst so an inbound composite signal reaches the action,
		// matching the scalar parity. dst is always addressable (both callers
		// pass reflect.ValueOf(&field).Elem()). Best-effort: a shape mismatch
		// (client sends a string for a []int signal) zeros dst rather than
		// erroring — the prior value is meaningless once the client diverges.
		//
		// Zero dst first so the decode is a full replace, not a merge:
		// json.Unmarshal into a non-nil map keeps pre-existing keys, so a
		// client that removed a SignalMap key would otherwise leave the stale
		// key alive server-side — diverging from the scalar replace contract.
		b, err := json.Marshal(raw)
		if err != nil {
			return fmt.Errorf("cannot marshal value: %v", err)
		}
		dst.Set(reflect.Zero(dst.Type()))
		if err := json.Unmarshal(b, dst.Addr().Interface()); err != nil {
			return fmt.Errorf("cannot decode into %s: %v", dst.Type(), err)
		}
	}
	return nil
}
