package via

import (
	"encoding/json"
	"reflect"
	"sync"
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

// upcastFn migrates a stored event payload from version N to version N+1.
type upcastFn = func(old json.RawMessage) (json.RawMessage, error)

// eventVersionInfo is the registered version history for one event type:
// current is the highest version the binary writes/reads; steps[from] migrates
// a version-`from` payload to `from+1`.
type eventVersionInfo struct {
	current int
	steps   map[int]upcastFn
}

// eventRegistry maps an event type to its version history. Written by
// RegisterEvent (at init/setup, before Mount) and read on the Append/fold hot
// path — guarded so a stray concurrent registration is race-clean.
var eventRegistry = struct {
	mu sync.RWMutex
	m  map[reflect.Type]*eventVersionInfo
}{m: make(map[reflect.Type]*eventVersionInfo)}

// RegisterEvent registers a single-step upcaster that migrates a version
// fromVersion payload of event type E to version fromVersion+1, so a stored
// record written before a RESHAPE still decodes into the current E. Call it at
// init/setup, before Mount. The current version of E becomes 1+max(fromVersion
// registered) (1 with none).
//
// Additive-first: you rarely need an upcaster. Adding a field needs none (JSON
// ignores unknown fields on decode and zero-fills missing ones); only a reshape
// — renaming, splitting, or changing the type of a field — needs one. The
// upcaster works on raw JSON and never touches Fold, so version logic stays at
// the codec boundary.
func RegisterEvent[E any](fromVersion int, upcast upcastFn) {
	key := reflect.TypeFor[E]()
	eventRegistry.mu.Lock()
	defer eventRegistry.mu.Unlock()
	info := eventRegistry.m[key]
	if info == nil {
		info = &eventVersionInfo{current: currentEventVersion, steps: map[int]upcastFn{}}
		eventRegistry.m[key] = info
	}
	info.steps[fromVersion] = upcast
	if fromVersion+1 > info.current {
		info.current = fromVersion + 1
	}
}

// currentVersionFor returns the version the binary stamps and reads for E:
// 1+max(registered fromVersion), or currentEventVersion (1) when unregistered.
func currentVersionFor[E any]() int {
	eventRegistry.mu.RLock()
	defer eventRegistry.mu.RUnlock()
	if info := eventRegistry.m[reflect.TypeFor[E]()]; info != nil {
		return info.current
	}
	return currentEventVersion
}

// runUpcasters applies E's registered upcaster steps in order from→to. A missing
// step or a failing upcaster yields ErrUndecodable, so an un-migratable record
// is dropped (drop-on-undecodable) rather than mis-folded.
func runUpcasters[E any](from, to int, d json.RawMessage) (json.RawMessage, error) {
	// Snapshot the steps we need UNDER the lock: the steps map is mutated by a
	// concurrent RegisterEvent, so reading it after releasing the lock would be a
	// data race. User upcaster fns run after the lock is dropped, off the map.
	steps := make([]upcastFn, 0, to-from)
	eventRegistry.mu.RLock()
	info := eventRegistry.m[reflect.TypeFor[E]()]
	if info != nil {
		for v := from; v < to; v++ {
			steps = append(steps, info.steps[v])
		}
	}
	eventRegistry.mu.RUnlock()
	if info == nil {
		if from == to {
			return d, nil
		}
		return nil, ErrUndecodable
	}
	for _, fn := range steps {
		if fn == nil {
			return nil, ErrUndecodable
		}
		next, err := fn(d)
		if err != nil {
			return nil, ErrUndecodable
		}
		d = next
	}
	return d, nil
}
