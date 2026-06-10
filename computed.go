package via

import "github.com/go-via/via/h"

// Computed defines a client-side derived signal named key whose value is
// recomputed from expr whenever any signal expr references changes. It maps
// to Datastar's data-computed-<key>; expr is a JS expression over other
// signals (referenced as $other). Derived values stay on the client — no
// server round-trip — so prefer it over re-deriving in View for display-only
// values (totals, formatted labels, validity flags):
//
//	via.Computed("full", "$first + ' ' + $last")
//	h.Span(c.Full.Text()) // $full is now bindable like any signal
//
// expr is emitted verbatim (HTML-escaped); it is a raw Datastar expression,
// not a typed Go value — reference signals by their wire key with a $ prefix.
func Computed(key, expr string) h.H {
	return h.Data("computed-"+key, expr)
}

// Effect runs expr reactively on the client whenever a signal it references
// changes, for side effects (logging, focus, third-party calls) rather than
// producing a value. It maps to Datastar's data-effect. expr is a raw
// Datastar expression emitted verbatim (HTML-escaped).
func Effect(expr string) h.H {
	return h.Data("effect", expr)
}
