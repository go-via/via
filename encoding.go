package via

import (
	"encoding/json"
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
	if raw == nil {
		return
	}
	switch dst.Kind() {
	case reflect.String:
		if s, ok := raw.(string); ok {
			dst.SetString(s)
		}
	case reflect.Bool:
		switch v := raw.(type) {
		case bool:
			dst.SetBool(v)
		case string:
			// `via:"open,init=true"` arrives as a string from the struct
			// tag; ParseBool covers "true"/"false"/"1"/"0" and friends.
			if b, err := strconv.ParseBool(v); err == nil {
				dst.SetBool(b)
			}
		}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		switch n := raw.(type) {
		case float64:
			dst.SetInt(int64(n))
		case int64:
			dst.SetInt(n)
		case int:
			dst.SetInt(int64(n))
		case string:
			if i, err := strconv.ParseInt(n, 10, 64); err == nil {
				dst.SetInt(i)
			}
		}
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		switch n := raw.(type) {
		case float64:
			dst.SetUint(uint64(n))
		case uint64:
			dst.SetUint(n)
		case string:
			if i, err := strconv.ParseUint(n, 10, 64); err == nil {
				dst.SetUint(i)
			}
		}
	case reflect.Float32, reflect.Float64:
		switch n := raw.(type) {
		case float64:
			dst.SetFloat(n)
		case string:
			if f, err := strconv.ParseFloat(n, 64); err == nil {
				dst.SetFloat(f)
			}
		}
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
		if b, err := json.Marshal(raw); err == nil {
			dst.Set(reflect.Zero(dst.Type()))
			_ = json.Unmarshal(b, dst.Addr().Interface())
		}
	}
}
