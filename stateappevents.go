package via

import (
	"context"
	"encoding/json"
	"reflect"

	"github.com/go-via/via/h"
)

// StateAppEvents is an app-scoped, event-sourced reactive value: the value is
// the fold of an append-only event log shared across every session and tab.
// Unlike StateApp[T] (which CAS-writes a single value), the projected value is
// derived purely from the log via E's Fold.
//
//	type Feed struct {
//	    Posts via.StateAppEvents[PostEvent, []Post]
//	}
//
// The zero value is usable: declare the field, no init. With a nil backplane it
// degrades to today's single-pod in-process behavior, no API difference.
//
// The handle holds only the wire key (and, once Read/Update land, the bound
// app); the log itself lives in the backplane owned by the via runtime,
// populated at Mount time.
type StateAppEvents[E EventReducer[E, V], V any] struct {
	wireKey string
	app     *App // bound at Mount; nil before
}

// EventReducer constrains E to be its own reducer: the fold lives on the event
// TYPE, as a single method. The fold SEED — the projected value of an EMPTY
// log — is the Go zero value of V; there is no Zero() method.
//
// Determinism rule (load-bearing): Fold MUST be a pure function of
// (acc, receiver) — no clock, no RNG, no I/O, no globals. Two pods replaying
// the same offset range must converge to the same V. A wall-clock or random
// input must be carried as a field on E, stamped at Append, never sampled
// inside Fold. Fold MUST return acc unchanged for an unknown event variant so a
// pod running old code that tails a new-variant event folds it as a no-op.
type EventReducer[E any, V any] interface {
	Fold(acc V, ev E) V
}

// bindWireKey writes the wire key into the handle at Mount time, via the same
// scopeBinder seam StateApp/StateSess use.
func (l *StateAppEvents[E, V]) bindWireKey(k string) { l.wireKey = k }

// bindApp binds the App and registers this key's typed seed + fold with the
// per-key projector (started once per key). Called by the runtime for log-kind
// scope slots only. Being a method on the typed handle is how the runtime
// bridges from a reflection-detected, type-erased field to the generic E/V fold
// (T1-GO-8): the closure captures E and V; the App stores only `any`.
func (l *StateAppEvents[E, V]) bindApp(a *App) {
	l.app = a
	var seed V
	fold := func(acc any, data []byte) (any, error) {
		// Decode through the shared version-envelope path (same as OnEvent
		// consumers), then fold; a decode error (undecodable / forward-incompat)
		// propagates to the projector's drop / halt handling unchanged.
		ev, err := decodeEvent[E](data)
		if err != nil {
			return acc, err
		}
		cur, _ := acc.(V)
		return ev.Fold(cur, ev), nil
	}
	// Snapshot codec: encode/decode the projected V to/from bytes (the App is
	// type-erased, so these closures carry V); codecHash invalidates the cache
	// if V's type changes (evolving V is then free — it re-folds from genesis).
	encodeSnap := func(p any) ([]byte, error) {
		v, _ := p.(V)
		return json.Marshal(v)
	}
	decodeSnap := func(b []byte) (any, error) {
		var v V
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, err
		}
		return v, nil
	}
	a.registerLog(l.wireKey, any(seed), fold, encodeSnap, decodeSnap, reflect.TypeFor[V]().String())
}

// Key returns the wire key (lowercase field name unless overridden by `via:` tag).
func (l *StateAppEvents[E, V]) Key() string { return l.wireKey }

// Read returns the current PROJECTED value: the fold of every event up to this
// pod's locally-applied offset, seeded by the Go zero of V. A Read during View
// execution subscribes the ctx via trackRead — IDENTICAL to StateApp.Read — so
// a later Append (folded by the projector) fans a re-render out to this tab.
// O(1): returns the cached projection; never re-folds from genesis. Accepts
// *Ctx or *CtxR.
func (l *StateAppEvents[E, V]) Read(rc readCtx) V {
	var zero V
	if rc == nil || l.app == nil {
		return zero
	}
	ctx := rc.rctx()
	if ctx == nil {
		return zero
	}
	ctx.trackRead(l.wireKey)
	v, ok := l.app.logProjection(l.wireKey)
	if !ok {
		return zero
	}
	t, _ := v.(V)
	return t
}

// Append commits ONE immutable event to the EventLog. Unlike StateApp.Update
// there is no read-modify-write and no old value: you describe WHAT HAPPENED,
// the fold derives the new value. Concurrent Appends never conflict (the
// EventLog orders them).
//
// Append does NOT fold and does NOT render: the per-(pod,key) projector is the
// SOLE fold path on every pod incl. the writer (T1-SRE-2). The writer's own
// View updates when ITS projector folds this offset (one in-process hop), so
// cross-tab read-your-write is eventual. The returned offset is the commit
// position.
//
// Panics on nil ctx, exactly like StateApp.Update — the ctx is the
// AUTHORIZATION gate (Append is reachable only from a via_tab + session-gated
// action ctx), and the projector, not the ctx, drives the re-render. A nil ctx
// means the call did not come from a legitimate tab action.
func (l *StateAppEvents[E, V]) Append(ctx *Ctx, ev E) (Offset, error) {
	if ctx == nil {
		panic("via: StateAppEvents.Append called with nil *Ctx")
	}
	if l.app == nil {
		return 0, nil // nil backplane pre-Mount: parity with StateApp's no-op guard
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		return 0, err
	}
	// Wrap in the versioned envelope so the event can be evolved later (an
	// event appended without an envelope can never be upcast — see T-DX-3).
	data, err := json.Marshal(eventEnvelope{T: eventTypeTag[E](), V: currentVersionFor[E](), D: payload})
	if err != nil {
		return 0, err
	}
	// context.Background for now — the in-memory backplane ignores it; wiring
	// the action's request context for cancellation is a later refinement.
	return l.app.backplane.Append(context.Background(), l.wireKey, data)
}

// Text returns the projected value as a text node. Sibling of StateApp.Text.
func (l *StateAppEvents[E, V]) Text(rc readCtx) h.H { return h.Textf("%v", l.Read(rc)) }

// stateAppEventsMarker tags StateAppEvents[E, V] (and types that embed it) with
// its OWN marker — distinct from stateAppMarker — so the walker can later start
// the per-key projector only for log keys, not value keys.
type stateAppEventsMarker interface{ isStateAppEvents() }

func (*StateAppEvents[E, V]) isStateAppEvents() {}
