package via

import (
	"encoding/json"
	"reflect"
	"strconv"

	"github.com/go-via/via/h"
)

// Signal is a typed reactive value mirrored to the browser. The value lives
// inside the composition struct; Get/Set go through the bound *Ctx so
// changes are tracked and propagated over SSE.
//
//	type Counter struct {
//	    Step via.Signal[int] `via:"step,init=1"`
//	}
//	c.Step.Get(ctx)        // returns int
//	c.Step.Set(ctx, 5)     // marks dirty, browser updates next flush
//	c.Step.Bind()          // <input> two-way bind: data-bind="step"
//	c.Step.Text()          // <span data-text="$step"></span>
//
// Untyped, untagged Signal[T] fields use the lower-cased field name as the
// wire key. Tag form: `via:"name,init=value"`; either part is optional.
type Signal[T any] struct {
	val    T
	slot   uint16
	key    string
	dollar string // "$" + key, precomputed for Text/Show — saves a concat per render
}

// Get returns the current value. The ctx is unused today but kept so
// every reactive-handle Get/Set has the same shape (and so future tab-
// scoped reads can move into the runtime without an API break).
func (s *Signal[T]) Get(_ *Ctx) T {
	return s.val
}

// Set writes a new value and marks the signal dirty so the next flush
// patches it to the browser. From inside an action method or a
// via.Stream callback, the flush is automatic. From a raw goroutine
// you started yourself, call ctx.Sync() at a coalescing boundary —
// the dirty bit alone won't reach the browser without a flush.
func (s *Signal[T]) Set(ctx *Ctx, v T) {
	s.val = v
	if ctx != nil {
		ctx.markSignalDirty(s.slot)
	}
}

// Update applies fn to the current value and stores the result. Saves
// a Get/Set pair on transform-the-current-value patterns.
func (s *Signal[T]) Update(ctx *Ctx, fn func(T) T) {
	if fn == nil {
		return
	}
	s.val = fn(s.val)
	if ctx != nil {
		ctx.markSignalDirty(s.slot)
	}
}

// Mutable is the common get/set surface shared by Signal[T] and State[T].
// Helpers like Toggle / Add accept any Mutable so they work uniformly on
// either flavor of reactive field — the wire-side difference (Signal
// mirrors to the browser, State stays server-side) doesn't matter for a
// pure read-modify-write.
type Mutable[T any] interface {
	Get(ctx *Ctx) T
	Set(ctx *Ctx, v T)
}

// Compile-time guards: every reactive handle in this package satisfies
// Mutable[T]. A breaking refactor (e.g. dropping Set) shows up here
// rather than at every call site that uses Toggle / Add.
var (
	_ Mutable[int]  = (*Signal[int])(nil)
	_ Mutable[bool] = (*State[bool])(nil)
)

// Toggle flips a boolean reactive field. Free function (rather than a
// method) so the type parameter can be locked down to bool — methods on
// a generic type can't constrain T.
//
//	func (p *Sidebar) ToggleOpen(ctx *via.Ctx) error {
//	    via.Toggle(ctx, &p.Open)
//	    return nil
//	}
func Toggle(ctx *Ctx, m Mutable[bool]) {
	if m == nil {
		return
	}
	m.Set(ctx, !m.Get(ctx))
}

// numeric is the constraint satisfied by every Go numeric type that
// supports + on a Signal[T] / State[T] value. Defined inline so we don't
// pull in golang.org/x/exp/constraints, and kept unexported because no
// caller outside this package names the constraint — Add's type parameter
// is inferred at call sites.
type numeric interface {
	~int | ~int8 | ~int16 | ~int32 | ~int64 |
		~uint | ~uint8 | ~uint16 | ~uint32 | ~uint64 |
		~float32 | ~float64
}

// SetIfChanged writes v to m only when it differs from the current
// value. Returns true if a write happened. Use it on chatty real-time
// streams where many ticks emit the same value — the unchanged ticks
// skip the dirty flag, SSE patch, and re-render entirely:
//
//	via.SetIfChanged(ctx, &p.NetRX, fmtBytes(rx)) // no-op when unchanged
//
// Constrained to comparable T; Signal/State of slices, maps, or other
// non-comparable shapes need their own equality and aren't covered here.
func SetIfChanged[T comparable](ctx *Ctx, m Mutable[T], v T) bool {
	if m == nil {
		return false
	}
	if m.Get(ctx) == v {
		return false
	}
	m.Set(ctx, v)
	return true
}

// Add increments a numeric reactive field by delta. delta may be negative.
//
//	via.Add(ctx, &p.Count, 1)   // increment
//	via.Add(ctx, &p.Count, -1)  // decrement
func Add[T numeric](ctx *Ctx, m Mutable[T], delta T) {
	if m == nil {
		return
	}
	m.Set(ctx, m.Get(ctx)+delta)
}

// Push appends item to a slice-valued Signal and marks it dirty so the
// next flush patches the new tail to the browser. Collapses the common
// get-append-set pattern for chart series, log feeds, and other
// append-only flows into one call:
//
//	via.Push(ctx, &p.Series, point)
//
// nil sig is a no-op. To cap retained items, use [PushBounded].
func Push[T any](ctx *Ctx, sig *Signal[[]T], item T) {
	if sig == nil {
		return
	}
	sig.val = append(sig.val, item)
	if ctx != nil {
		ctx.markSignalDirty(sig.slot)
	}
}

// PushBounded is [Push] with a hard cap on retained items. Once the
// slice would exceed max, the oldest entries are dropped so only the
// most recent max items remain. max <= 0 is a no-op (nothing is
// appended, nothing is marked dirty).
func PushBounded[T any](ctx *Ctx, sig *Signal[[]T], item T, max int) {
	if sig == nil || max <= 0 {
		return
	}
	sig.val = append(sig.val, item)
	if len(sig.val) > max {
		// Shift-left to keep the backing array; copy handles the overlap.
		copy(sig.val, sig.val[len(sig.val)-max:])
		sig.val = sig.val[:max]
	}
	if ctx != nil {
		ctx.markSignalDirty(sig.slot)
	}
}

// Bind returns a two-way binding attribute. Use on form inputs.
func (s *Signal[T]) Bind() h.H {
	return h.Data("bind", s.key)
}

// Text returns a reactive text span: <span data-text="$key"></span>.
func (s *Signal[T]) Text() h.H {
	return h.Span(h.Data("text", s.dollar))
}

// Show returns a data-show attribute that toggles display by truthiness.
func (s *Signal[T]) Show() h.H {
	return h.Data("show", s.dollar)
}

// Attr returns a data-attr-<name> attribute that mirrors this signal's
// truthiness onto the host element's HTML attribute. Truthy → attribute
// present (boolean form, e.g. `disabled`); falsy → attribute absent.
// For string-valued attributes, the attribute value tracks the signal.
//
//	h.Button(c.Saving.Attr("disabled"), h.Text("Save"))
//	h.A(c.Target.Attr("href"), h.Text("Open"))
func (s *Signal[T]) Attr(name string) h.H {
	return h.Data("attr-"+name, s.dollar)
}

// Style returns a data-style-<prop> attribute that drives an inline CSS
// property from this signal's stringified value. Pairs naturally with
// `Signal[string]` carrying a colour, length, etc.
//
//	h.Div(c.Hue.Style("background-color"))
func (s *Signal[T]) Style(prop string) h.H {
	return h.Data("style-"+prop, s.dollar)
}

// Key returns the wire key (qualified field path). Useful in tests.
func (s *Signal[T]) Key() string { return s.key }

// signalRef is the internal interface implemented by every Signal[T] /
// State[T] handle. It lets the runtime perform reflection-free per-request
// initialization across mixed-type fields.
type signalRef interface {
	bindSlot(slot uint16, key string)
	encode() ([]byte, error)
	decodeRaw(raw any)
}

func (s *Signal[T]) bindSlot(slot uint16, key string) {
	s.slot = slot
	s.key = key
	s.dollar = "$" + key
}

func (s *Signal[T]) encode() ([]byte, error) {
	return encodeScalar(reflect.ValueOf(s.val))
}

func (s *Signal[T]) decodeRaw(raw any) {
	decodeScalarInto(reflect.ValueOf(&s.val).Elem(), raw)
}

// jsonTrue / jsonFalse cache the only two possible Bool encodings so we
// don't reallocate the same 4 / 5 bytes on every render. The bytes are
// fed to json.RawMessage in writePageDocument which never mutates them.
var (
	jsonTrue  = []byte("true")
	jsonFalse = []byte("false")
)

// scalar JSON encoder, no fmt.Sprintf. Falls back to encoding/json for
// composites (handled in state.go via reflect path).
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
		return strconv.AppendFloat(nil, v.Float(), 'g', -1, 64), nil
	}
	return json.Marshal(v.Interface())
}

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
	}
}
