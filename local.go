package via

import (
	"encoding/json"

	"github.com/go-via/via/h"
)

// LocalSignal is a client-only reactive value — a Datastar `_`-prefixed signal
// that lives entirely in the browser, is never sent to the server, and needs
// no composition struct field or Mount binding. Use it for ephemeral UI state
// (menu open/closed, hover, active tab) that should react instantly without a
// server round-trip:
//
//	open := via.Local("open")
//	h.Div(open.Init(false),
//	    h.Button(open.Toggle(), h.Text("menu")),
//	    h.Nav(open.Show(), ...),
//	)
//
// For state the server must see or persist, use a [Signal] or State* handle
// instead — a LocalSignal is intentionally invisible to actions.
type LocalSignal struct {
	name   string // wire name without the leading "_"
	dollar string // "$_" + name, the expression reference
}

// Local returns a client-only signal handle named "_"+name. name must be a
// valid JS identifier fragment (letters, digits, underscores).
func Local(name string) LocalSignal {
	return LocalSignal{name: name, dollar: "$_" + name}
}

// Init declares the signal with an initial value via Datastar's object form
// (data-signals="{_name:<json>}"). Place it once on a container element that
// wraps the uses of this signal.
func (l LocalSignal) Init(v any) h.H {
	// json.Marshal only fails for un-encodable kinds (chan/func) — a caller
	// error, surfaced loud at first render rather than emitting a broken signal.
	b, err := json.Marshal(v)
	if err != nil {
		panic("via.Local(" + l.name + ").Init: value cannot be JSON-encoded: " + err.Error())
	}
	return h.Data("signals", "{_"+l.name+":"+string(b)+"}")
}

// Ref returns the "$_name" expression for use in raw Datastar expressions.
func (l LocalSignal) Ref() string { return l.dollar }

// Bind returns a two-way binding attribute for form inputs (data-bind="_name").
func (l LocalSignal) Bind() h.H { return h.Data("bind", "_"+l.name) }

// Text binds the signal's value as the host element's text content.
func (l LocalSignal) Text() h.H { return h.Data("text", l.dollar) }

// Show toggles the host element's display by the signal's truthiness.
func (l LocalSignal) Show() h.H { return h.Data("show", l.dollar) }

// ShowUnless is the negation of [LocalSignal.Show].
//
// EXPERIMENTAL: a young convenience helper; may change before 1.0.
func (l LocalSignal) ShowUnless() h.H { return h.Data("show", "!"+l.dollar) }

// Class toggles the named CSS class by the signal's truthiness. See
// [Signal.Class] for the lower-case attribute-name caveat.
func (l LocalSignal) Class(name string) h.H { return h.Data("class:"+name, l.dollar) }

// Toggle returns an on:click attribute that flips a boolean local signal —
// the canonical client-only toggle with no server round-trip.
func (l LocalSignal) Toggle() h.H { return h.Data("on:click", l.dollar+"=!"+l.dollar) }
