package via

import (
	"encoding/json"
	"reflect"
)

// currentEventVersion is the highest event-envelope version this binary writes
// and understands. A record with a higher version is ErrForwardIncompatible (a
// newer binary wrote it); v1 has no upcasters, so any record this binary can
// read is version 1.
const currentEventVersion = 1

// eventEnvelope is the self-describing wrapper every StateAppEvents event rides
// in, so events can be evolved without rewriting history. T is a diagnostic
// type tag (auto-derived from E's Go type in v1 — see T-DX-3); V is the
// load-bearing version that drives the (future) upcaster chain and the
// forward-incompatibility guard; D is the event's own JSON payload.
type eventEnvelope struct {
	T string          `json:"t"`
	V int             `json:"v"`
	D json.RawMessage `json:"d"`
}

// eventTypeTag returns the auto-derived (diagnostic) type tag for E.
func eventTypeTag[E any]() string { return reflect.TypeFor[E]().String() }
