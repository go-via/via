package via

import (
	"encoding/json"
	"fmt"
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
// Mutation grammar: StateAppEvents uses DIRECT verbs — l.Append(ctx, ev) and
// l.Read(ctx) — not the l.Op(ctx).Verb() grammar that the collection shapes
// (StateAppSlice/StateAppMap) use. That is deliberate, not an oversight: those
// shapes expose MANY mutators (Append/Insert/Delete/Set/…) and route them
// through a single Op(ctx) so the ctx is captured once; StateAppEvents has
// exactly ONE mutator (Append), so a direct ctx-first method is both simpler and
// consistent with its true peer, StateApp.Update(ctx, …), which is also a direct
// ctx-first verb. One verb → one method.
//
// The handle holds only the wire key and the bound app; the log itself lives in
// the backplane owned by the via runtime, populated at Mount time.
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
		ev, err := decodeEvent[E](data, a.eventDecryptor())
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
//
// Error surface: returns a non-nil error only if the event cannot be encoded
// (a json.Marshal failure on ev — a programming error, surfaced rather than
// panicked) or the backplane rejects the append (e.g. ErrClosed during
// Shutdown). It returns (0, nil) — a deliberate no-op — before Mount when no
// backplane is bound, parity with StateApp's pre-Mount guard. Most call sites
// can ignore the error (as the chat example does); handle it where a failed
// append must be surfaced to the user (e.g. a form that should report "not
// saved").
func (l *StateAppEvents[E, V]) Append(ctx *Ctx, ev E) (Offset, error) {
	if ctx == nil {
		panic("via: StateAppEvents.Append called with nil *Ctx")
	}
	if l.app == nil {
		return 0, nil // nil backplane pre-Mount: parity with StateApp's no-op guard
	}
	data, err := marshalEvent(l.app, ev)
	if err != nil {
		return 0, err
	}
	// backplaneCtx is cancelled on Shutdown so a wedged backend's Append aborts
	// with the drain rather than blocking the action goroutine forever.
	return l.app.backplane.Append(l.app.backplaneCtx, l.wireKey, data)
}

// marshalEvent builds the wire record for one event: marshal the payload, wrap
// it in the versioned envelope (so it can be evolved later — T-DX-3), and, when
// a KeyStore is configured and the event implements DataSubject, encrypt the
// payload under that subject's key (crypto-shred). Shared by Append and tests so
// both produce identical records.
func marshalEvent[E any](a *App, ev E) (out []byte, err error) {
	// Single origin prefix for every failure path (payload encode, key lookup,
	// encrypt, envelope encode) — this is StateAppEvents.Append's error surface,
	// so a bare "json: unsupported type" must not reach an operator unlabelled.
	defer func() {
		if err != nil {
			err = fmt.Errorf("via: marshal event: %v", err)
		}
	}()
	payload, err := json.Marshal(ev)
	if err != nil {
		return nil, err
	}
	env := eventEnvelope{T: eventTypeTag[E](), V: currentVersionFor[E](), D: payload}
	if ks := a.cfg.keyStore; ks != nil {
		if ds, ok := any(ev).(DataSubject); ok {
			if subject := ds.DataSubject(); subject != "" {
				key, err := ks.KeyFor(a.backplaneCtx, subject)
				if err != nil {
					return nil, err
				}
				token, err := encryptPayload(key, payload)
				if err != nil {
					return nil, err
				}
				env.S, env.D = subject, token
			}
		}
	}
	return json.Marshal(env)
}

// Text returns the projected value as a text node. Sibling of StateApp.Text.
// Accepts either *Ctx (action handlers) or *CtxR (View).
func (l *StateAppEvents[E, V]) Text(rc readCtx) h.H { return h.Textf("%v", l.Read(rc)) }

// stateAppEventsMarker tags StateAppEvents[E, V] (and types that embed it) with
// its OWN marker — distinct from stateAppMarker — so the walker can later start
// the per-key projector only for log keys, not value keys.
type stateAppEventsMarker interface{ isStateAppEvents() }

func (*StateAppEvents[E, V]) isStateAppEvents() {}
