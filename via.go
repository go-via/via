// Package via is a server-driven reactive UI toolkit built on the h DSL and the
// Datastar client: plain request/response pages, SSE-backed live children,
// server-authoritative State/List/Signal, and always-on sessions.
//
// Hard guarantees (the point of the design): no '&' at any user call site, no
// reflection in the public API surface (one reflect call on the action path, in
// action.go, reads a handler's own func name to address its action), no
// closures in it either, no any in element/child signatures. The library is
// stdlib-only. Identifier strings do appear at the edges the caller controls
// directly — ctx.Param[T]("id"), FormFile("avatar"), Mount("/thread/{id}") —
// but never as an internal wire-name a caller could desync (see the
// Field-Embeddable Types convention).
//
// # Lifecycle hooks
//
// The hooks are duck-typed: opt in by having the method, so a rename or
// signature change silently opts a unit back out. [Mount] and [Child] catch
// two slips — a hook-named method with the wrong signature panics at boot,
// and a near-miss name carrying a hook's exact signature is logged once. A
// near miss is a known alias ("Reload", "Init") or a hook name one typing slip
// away ("OnRelaod", "Oninit"). A leftover v0.7 OnConnect(*via.Ctx) error is
// logged too, even next to an OnInit.
//
//	OnInit(*via.Ctx) error   // before the ctx-free View, on a page or any
//	                         // embedded child
//	OnReload(*via.Ctx) error // after one of the unit's actions, before the
//	                         // render that answers it
//	PageMeta() via.Meta      // the mounted root's own document
//
// OnInit loads request or session data into a unit's fields and registers
// the timers and subscriptions that make it live — [Ctx.Tick] and
// [Ctx.Listen] are valid only there. It runs on every transport but a live
// action over an already-open stream: it ran once, at connect, so a session
// whose authorization changes after that keeps acting on the stream until
// the tab next acts and is denied, the stream closes, or the router shuts
// down.
//
// OnReload is the fix for the commonest week-one defect — OnInit loads, the
// handler mutates the store, and the render answering the action still shows
// what OnInit loaded. It is a second hook rather than a second OnInit run
// because OnInit is an initializer, not a loader: it mints and defaults the
// session, registers Tick/Listen, and may Redirect or return [ErrNotFound],
// all wrong to repeat once a handler has committed a mutation. It runs on
// the plain path and the live path alike, once per action, and is skipped
// when the handler queued a Redirect. Tick and Listen are no-ops inside it:
// liveness is the GET/connect verdict. A non-nil error is answered like
// OnInit's — ErrNotFound is 404, anything else 500.
//
// PageMeta names the document — title, description, social cards, assets —
// and is read after OnInit and OnReload, so the data is already loaded.
// Only the mounted root's is read, and it takes effect on a render that
// writes a document (the GET, and the full-page answer to a native form
// submit), never on an SSE push. See [Meta].
//
// # Goroutine model
//
// A composition instance is never shared between goroutines by via, and none
// of its handles take a lock. That is safe because every callback via runs
// against one instance runs on one goroutine:
//
//   - A plain request (a GET page, an action POST on a page with no live unit)
//     gets its own instance, copied from the value passed to [Mount].
//     OnInit, the action handler, OnReload and View all run on that request's
//     net/http goroutine, and the instance is discarded with the response.
//   - A live connection (one opened by [Ctx.Tick], [Ctx.Listen], or by
//     rendering a [State] or [List]) keeps its instance for the life of the
//     stream, and everything that touches it runs on that stream's single
//     goroutine: every Tick handler, every Listen handler, every re-render and
//     every SSE write. A [Ctx.Tick] timer does own a goroutine, but it only
//     posts work onto the stream goroutine; it never calls your fn itself.
//     An action POST against a live tab is likewise marshalled onto the stream
//     goroutine and its HTTP handler waits for the result, so a handler never
//     runs concurrently with a tick.
//
// The rule that follows: [Signal], [State], [List], [Ctx] and [Session] are
// Not safe for concurrent use. Call them only from a via callback. To reach a
// unit from a goroutine of your own — a background worker, a message consumer,
// an http.Handler outside via — publish to a [topic.Topic] and have the unit
// [Ctx.Listen] to it; the value is then delivered on the unit's own goroutine.
//
//	go func() { prices.Publish(tick) }() // fine: Topic is concurrency-safe
//	go func() { p.Price.Set(tick) }()    // race: Set is not
package via

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"log/slog"
	"maps"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"encoding/json"
	"net/http"
	"net/url"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// datastarJS is the vendored Datastar client, served at /_via/datastar.js.
//
//go:embed datastar.js
var datastarJS []byte

// datastarHash names this build's client. Pages load it as
// /_via/datastar.js?v=<hash>, so the versioned URL can be cached as immutable:
// a new client is a new URL. The bare path keeps working for anything that
// hard-coded it, and revalidates against the same hash as its ETag.
var datastarHash = func() string {
	sum := sha256.Sum256(datastarJS)
	return hex.EncodeToString(sum[:8])
}()

// viewer is the (pointer) contract a root must satisfy: a pure, ctx-free View.
type viewer interface{ View() h.H }

// instance is one root/child composition plus the base address and size a
// Signal names itself by. Both come from the generic entry point (Mount's T,
// Child's C); everything downstream is non-generic and would otherwise reflect.
type instance struct {
	v    viewer
	base unsafe.Pointer
	size uintptr
	typ  reflect.Type // the composition's struct type, for the field-name signal table
	sig  *typeSignals // that table, resolved once per type (never per render)
	// Carried on the instance, not derived from the child key: a live child's
	// push re-renders the child with no parent in scope and must still mint
	// the exact same names the full-page walk did.
	slotPrefix string
	// ident is set on a child its parent cannot name by field (see
	// childViewer), for the same reason: a push must ship the URLs the walk did.
	ident string
}

// slotID is embedded first in Signal[T] so prebindSignals can stamp it through
// one pointer add without knowing T: no reflect on the render path.
type slotID struct {
	slot  string // stable wire name
	bound *Ctx   // the pass whose dirty map ships the patch
}

func (*slotID) isViaSignal() {}

var signalMarker = reflect.TypeOf((*interface{ isViaSignal() })(nil)).Elem()

// clientSignal is what SignalCS[T] satisfies and Signal[T] does not. csZero
// carries T out of the generic type: SignalCS has no T-typed field, so reflect
// alone cannot recover the zero value an untagged slot is declared with.
type clientSignal interface {
	isViaSignalCS()
	csZero() any
}

// seeded is the one mechanism behind via:"init=<json>", implemented by
// *Signal[T], *SignalCS[T] and *State[T] — and by *List[E] through the State it
// embeds first, so the pointer the applier receives is the embedded State's
// address too. decodeSeed carries T out of the generic type, and seedApplier
// builds the typed write once, on the Mount walk, so a seed costs a closure
// call per unit and no reflection.
//
// The applier writes only into a handle whose start value is not decided yet —
// a StateOf literal wins over the tag, and a later pass over the same instance
// must not undo a Set — and answers with the value the handle now holds, which
// is what a Signal's slot is declared at.
type seeded interface {
	decodeSeed(raw string) (any, error)
	seedApplier(v any) func(handle unsafe.Pointer) any
}

var seededMarker = reflect.TypeOf((*seeded)(nil)).Elem()

var viewerType = reflect.TypeOf((*viewer)(nil)).Elem()

// typeSignals is one composition type's signal table: every Signal-typed
// field's byte offset paired with the wire name its Go field path gives it.
type typeSignals struct {
	fields []signalField
	seeds  []seedField  // State/List fields carrying a via:"init=…" tag: no wire name, only a value to write
	tracks []trackField // every State/List field, so a StateTrack literal on any of them registers at init
	byOff  map[uintptr]signalField
	names  map[string]bool // every minted slot name, for the child-prefix collision check
}

type signalField struct {
	off      uintptr
	name     string
	cs       bool
	seed     any                      // resolved on the type walk so the render never reflects — a cs field's T zero, or the tag's JSON
	declares bool                     // every cs field, and a tagged Signal: its slot reaches the client with no Bind or Display
	apply    func(unsafe.Pointer) any // tagged Signal only; a SignalCS holds no value to write
}

type seedField struct {
	off   uintptr
	apply func(unsafe.Pointer) any
}

type trackField struct {
	off   uintptr
	start func(unsafe.Pointer, *Ctx)
}

// wire is the slot name: the "_" goes ahead of the scope prefix, because
// Datastar's fetch filter excludes /(^|\.)_/ and only a leading underscore
// keeps a nested or child-scoped signal off the POST too.
func (f signalField) wire(prefix string) string {
	if f.cs {
		return "_" + prefix + f.name
	}
	return prefix + f.name
}

var typeSignalCache sync.Map // reflect.Type -> *typeSignals

// signalsOf builds (memoized per type) the offset -> wire-name table: the Go
// field name, first rune lowercased, "_"-joined through plain nested structs.
// The walk stops at a field with its own View: that unit names its signals in
// its own table, under its child scope prefix.
//
// "_" and not "." is deliberate: Datastar treats a dotted signal name as a path
// and re-nests it, so "chat.draft" would arrive back as {"chat":{"draft":…}}
// and never match the flat slot the server looks up.
func signalsOf(t reflect.Type) *typeSignals {
	if v, ok := typeSignalCache.Load(t); ok {
		return v.(*typeSignals)
	}
	ts := &typeSignals{byOff: map[uintptr]signalField{}}
	minted := map[string]bool{}
	var walk func(t reflect.Type, base uintptr, prefix string, depth int)
	walk = func(t reflect.Type, base uintptr, prefix string, depth int) {
		if t == nil || t.Kind() != reflect.Struct || depth > 8 { // depth: a self-referential composition must not spin here
			return
		}
		for i := range t.NumField() {
			f := t.Field(i)
			name, off := prefix+lowerFirst(f.Name), base+f.Offset
			tag, tagged := f.Tag.Lookup("via")
			if reflect.PointerTo(f.Type).Implements(signalMarker) {
				sf := signalField{off: off, name: name}
				handle := reflect.New(f.Type).Interface()
				if cs, ok := handle.(clientSignal); ok {
					sf.cs, sf.seed, sf.declares = true, cs.csZero(), true
				}
				if tagged {
					sf.seed, sf.declares = decodeSeedTag(t, f, handle.(seeded), tag), true
					if !sf.cs {
						sf.apply = handle.(seeded).seedApplier(sf.seed)
					}
				}
				slot := sf.wire("")
				checkSlotName(t, f.Name, slot, minted)
				minted[slot] = true
				ts.fields = append(ts.fields, sf)
				ts.byOff[off] = sf
				continue
			}
			if reflect.PointerTo(f.Type).Implements(seededMarker) { // State, List
				handle := reflect.New(f.Type).Interface()
				if tagged {
					h := handle.(seeded)
					v := decodeSeedTag(t, f, h, tag)
					ts.seeds = append(ts.seeds, seedField{off: off, apply: h.seedApplier(v)})
				}
				// Every State lands here, tracked or not: whether this instance
				// carries a StateTrack literal is a per-instance fact the type
				// cannot answer, so the starter nil-checks at init.
				if tk, ok := handle.(tracker); ok {
					ts.tracks = append(ts.tracks, trackField{off: off, start: tk.trackStarter()})
				}
				continue
			}
			// Not descended: an entry here is a dead slot that also rebinds the
			// child's handles to the root's on every root render.
			if reflect.PointerTo(f.Type).Implements(viewerType) {
				checkNoSeedTag(t, f)
				continue
			}
			if f.Type.Kind() == reflect.Struct {
				checkNoSeedTag(t, f)
				walk(f.Type, off, name+"_", depth+1)
				continue
			}
			checkNoSeedTag(t, f)
			checkSignalReachable(t, f)
		}
	}
	walk(t, 0, "", 0)
	ts.names = minted
	typeSignalCache.Store(t, ts)
	return ts
}

// decodeSeedTag resolves a handle field's declared start value from its tag.
// A tag that fails to decode is a typo caught here, at Mount, rather than
// surfacing as a handle quietly holding a value nobody wrote.
func decodeSeedTag(owner reflect.Type, f reflect.StructField, h seeded, tag string) any {
	v, err := h.decodeSeed(parseSeedTag(owner, f, tag))
	if err != nil {
		panic("via: " + owner.String() + "." + f.Name + " has a via:\"init=…\" value that is not JSON " +
			"for its " + f.Type.String() + " type: " + err.Error())
	}
	return v
}

// parseSeedTag splits the one key via knows off the tag. The value runs to the
// end of the tag rather than to the next comma, so an object or array seed
// needs no quoting of its own commas.
func parseSeedTag(owner reflect.Type, f reflect.StructField, tag string) string {
	key, raw, ok := strings.Cut(tag, "=")
	if !ok || key != "init" {
		panic("via: " + owner.String() + "." + f.Name + " has via:" + strconv.Quote(tag) +
			": the only key is init=<json>")
	}
	return raw
}

// checkNoSeedTag panics when a via tag sits on a field that cannot use it —
// a plain field, a nested struct field — where its value is silently never
// applied.
func checkNoSeedTag(owner reflect.Type, f reflect.StructField) {
	if _, ok := f.Tag.Lookup("via"); ok {
		panic("via: " + owner.String() + "." + f.Name + " has a via:\"…\" tag, but only " +
			"a Signal, SignalCS, State or List field reads it")
	}
}

// checkSlotName panics on a duplicate slot name: two fields minting the same
// name (a nested A.B and a sibling A_b) share one hydrator entry, so a POST
// writes whichever the map kept — a silent wrong-field write.
func checkSlotName(t reflect.Type, field, name string, minted map[string]bool) {
	if minted[name] {
		panic("via: signal slot " + name + " is minted twice by " + t.String() +
			" (field " + field + ") — nested struct names join with \"_\", so rename one of the colliding fields")
	}
}

// checkSignalReachable panics when a field hides a Signal behind an indirection.
// A Signal is named by its byte offset in the composition, so one reached
// through a pointer, slice, array or map has no name at all: its writes land on
// memory the render discards. That used to surface only when the View first
// rendered it — a per-request 500 for the life of the process — so it is caught
// here instead, on the type walk Mount and Child do once.
//
// An interface-held Signal cannot be seen from the type, so that one case stays
// a render-time panic.
func checkSignalReachable(owner reflect.Type, f reflect.StructField) {
	switch f.Type.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
	default:
		return
	}
	if !typeHoldsSignal(f.Type, map[reflect.Type]bool{}, 0) {
		return
	}
	panic("via: " + owner.String() + "." + f.Name + " holds a via.Signal behind a " +
		f.Type.Kind().String() + " — a Signal is named by its field offset, so it must be a plain " +
		"struct field of the composition (through plain nested structs if you like), never behind a " +
		"pointer, slice, array, map or interface")
}

func typeHoldsSignal(t reflect.Type, seen map[reflect.Type]bool, depth int) bool {
	if t == nil || depth > 8 || seen[t] {
		return false
	}
	seen[t] = true
	if reflect.PointerTo(t).Implements(signalMarker) {
		return true
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return typeHoldsSignal(t.Elem(), seen, depth+1)
	case reflect.Struct:
		for i := range t.NumField() {
			if typeHoldsSignal(t.Field(i).Type, seen, depth+1) {
				return true
			}
		}
	}
	return false
}

func lowerFirst(s string) string {
	if s == "" || s[0] < 'A' || s[0] > 'Z' {
		return s
	}
	return string(s[0]+('a'-'A')) + s[1:]
}

// prebindSignals stamps every field-held Signal with its wire name and this
// render's Ctx before OnInit and the View run, so Ref reads a real name
// wherever it is called, a Child's by-value copy carries the child's prefix
// rather than the parent's last binding, and a Set on a signal the View never
// renders still has a pass to ship its patch.
func prebindSignals(c *Ctx, inst instance) {
	if inst.sig == nil || inst.base == nil {
		return
	}
	prefix := c.scopePrefix()
	for _, f := range inst.sig.fields {
		sid := (*slotID)(unsafe.Add(inst.base, f.off))
		sid.slot, sid.bound = f.wire(prefix), c
		if !f.declares {
			continue
		}
		// A tagged Signal is declared at what it holds, not at the seed: this
		// runs on every render, and re-declaring the seed over a value OnInit
		// or an action has since Set would undo it. A SignalCS holds nothing,
		// no Set will ever declare it, and a data-show may be its only reader,
		// so its seed is declared on every pass.
		v := f.seed
		if f.apply != nil {
			v = f.apply(unsafe.Add(inst.base, f.off))
		}
		c.declareSignal(sid.slot, v)
	}
	for _, s := range inst.sig.seeds {
		s.apply(unsafe.Add(inst.base, s.off))
	}
}

// startTracks registers each StateTrack literal in inst, ahead of runOnInit's
// or initChild's own OnInit call so a seeded State is what OnInit reads.
//
// inInit is raised around the loop because Track, Listen and OnConnect all
// reject a call from outside init; it is restored rather than cleared so the
// caller's own bracketing stays the one that decides.
func startTracks(c *Ctx, inst instance) {
	if inst.sig == nil || inst.base == nil || len(inst.sig.tracks) == 0 {
		return
	}
	was := c.inInit
	c.inInit = true
	defer func() { c.inInit = was }()
	for _, t := range inst.sig.tracks {
		t.start(unsafe.Add(inst.base, t.off), c)
	}
}

// childPrefixKey memoizes the parent-type -> child-type field-name lookup.
type childPrefixKey struct{ parent, child reflect.Type }

var childPrefixes sync.Map // childPrefixKey -> string ("" = ambiguous)

// childFieldName is the parent field a Child's child came from. Child takes
// the child by value, so the copy's address says nothing about which field it
// came from; the type does, as long as the parent holds exactly one field of
// it. Two fields of that type answer "" (Child's argument order need not match
// declaration order), and the caller falls back to the positional child key.
func childFieldName(parent, child reflect.Type) string {
	if parent == nil || child == nil || parent.Kind() != reflect.Struct {
		return ""
	}
	k := childPrefixKey{parent, child}
	if v, ok := childPrefixes.Load(k); ok {
		return v.(string)
	}
	name := ""
	for i := range parent.NumField() {
		if parent.Field(i).Type != child {
			continue
		}
		if name != "" {
			name = "" // more than one candidate: ambiguous
			break
		}
		name = lowerFirst(parent.Field(i).Name)
	}
	if name != "" {
		// A child scopes its child's slots under name+"__", while a plain
		// field literally named A__b mints "a__b" in the parent — same slot,
		// two different fields.
		for own := range signalsOf(parent).names {
			if strings.HasPrefix(own, name+"__") {
				panic(hcore.Miswired("via: signal slot " + own + " on " + parent.String() +
					" collides with the child prefix of field " + name + " — rename the field"))
			}
		}
	}
	childPrefixes.Store(k, name)
	return name
}

// ptrViewer is the *T constraint Handler/Mount share. Exported so `go doc`
// renders it at their signatures; an unexported alias showed up there as a
// bare, unresolvable name.
type ptrViewer[T any] = interface {
	*T
	viewer
}

// Ctx is the per-request binder: it names signal slots by field offset and
// actions by handler identity during a render pass, hydrates signals from the
// request, and records per-slot initial values.
//
// Not safe for concurrent use. Call it only from the via callback it was
// handed to; to reach a live unit from a goroutine of your own, publish to a
// [topic.Topic] the unit Listens to. See the package doc for the goroutine
// model.
type Ctx struct {
	order       []string                              // slots in assignment order
	initial     map[string]any                        // per-slot value seen at render time
	actions     map[string]action                     // content-addressed action table, keyed by handler identity
	hydrators   map[string]func(json.RawMessage) bool // per-slot updater, kept from the last render so a live action hydrates without re-rendering
	ticks       []tickReg
	subs        []subStarter
	onConnect   []func() // run once when the stream opens
	disposers   []func() // run on disconnect
	live        bool     // OnInit registered a Tick/Listen, or the View rendered server State
	dirty       map[string]any
	declareOnly map[string]any  // when non-nil, declare only these slots
	declareSeen map[string]bool // slots the pre-action render declared; one absent from it is seeded even when declareOnly excludes it
	req         *http.Request
	sessions    *sessionManager
	policy      *routerPolicy
	sessW       http.ResponseWriter // nil once the connect response is flushed, so a Tick/Listen Ctx's Session().Put warns instead of writing a dead response (I2)
	session     *Session
	children    []*Ctx // embedded children, in positional order (parent binder only)
	isChild     bool
	childKey    string // ordinal among the parent's Children, composed with the parent's key ("0", "0-0")
	unitV       instance
	rendered    []byte // inner HTML from the discovery render (for the 204 compare)
	push        func() // re-render this child and frame it on the stream
	declare     bool   // this render declares page-level data-signals
	base        string // mount path prefix for action POSTs
	redirect    redirectTo
	doInit      bool   // request-scoped, so every embedded child's OnInit runs before its View
	actedKey    string // child key of the unit an action just mutated; Child re-uses that instance instead of re-copying the parent's pristine field
	actedInst   instance
	// passUnits is dispatchPlain's discovery-only record of each child key's
	// first-pass Ctx, shared by the whole tree. Later passes render that pass's
	// instance again instead of a fresh copy off the parent's field, so a
	// child's hydrated values survive into the next pass the way a root's do.
	passUnits map[string]*Ctx
	inInit    bool            // true only while OnInit runs; outside it a Tick/Listen would register into a snapshot nobody reads
	reinit    bool            // this Ctx is the post-action re-run of OnInit: load again, register nothing (I5)
	errPage   bool            // this Ctx belongs to a WithErrorPage render: no mount, no route, no response of its own
	viewRan   bool            // the View has run: a Set from here on is a change to patch, not a seed to declare
	rev       *revertSet      // live only: how to put the server-authored signal values back after a display render (see livePush)
	streamCtx context.Context // live only: the connection's context, so Ctx.Context outlives the POST that req carries
	guard     *pushGuard      // live only: the connection's panic dedupe, which a Listen handler's recover reports to

	// badDecodeLogged dedupes the hydrator's decode-failure warning for the
	// whole tree this Ctx belongs to: a shared pointer, allocated once per
	// request root (or per connection, for a live unit) and carried by every
	// child and rebind so a malformed value posted for several slots at once
	// still logs one Warn, not one per slot per hydration pass.
	badDecodeLogged *atomic.Bool
}

// Request returns the HTTP request that triggered this handler — headers,
// cookies, URL, RemoteAddr, TLS. Nil when no request is in scope (a bare
// render).
//
// Read-only: the body is already consumed into the request's signals, and a
// live action runs on the stream goroutine after the POST has acked, so the
// request's Context may already be done.
//
// There is no matching writer. Ctx cannot set a header, a status or a response
// body — [Ctx.Redirect] is the only response shaping a handler gets — because a
// live action's "response" is an SSE frame on a connection the POST does not
// own. So anything that streams bytes to the browser (a file download, a CSV
// export, an image) is a sibling net/http handler, registered next to the via
// one and linked to like any other URL:
//
//	mux.Handle("/app/", viaRouter)
//	mux.HandleFunc("/files/{id}", serveAttachment) // http.ServeContent, not via
func (c *Ctx) Request() *http.Request { return c.req }

// Context returns the context that bounds this unit's work. On a live unit it
// is the stream context — cancelled when the tab disconnects or [Router.Shutdown]
// runs — which is the one a Tick or Listen handler can watch to abandon a slow
// call instead of finishing it against a dead socket:
//
//	func (p *Page) tick(ctx *via.Ctx) {
//		rows, err := p.db.QueryContext(ctx.Context(), …)
//		…
//	}
//
// Outside a live unit it is the request's own context, and context.Background
// when there is no request in scope (a bare render). It is never nil.
//
// Note the asymmetry with [Ctx.Request]: a live action's req.Context may
// already be done by the time the handler runs on the stream goroutine, so
// prefer this for anything that outlives the POST.
func (c *Ctx) Context() context.Context {
	if c.streamCtx != nil {
		return c.streamCtx
	}
	if c.req != nil {
		return c.req.Context()
	}
	return context.Background()
}

// logger is the Router's logger. A bare render has no router policy, so it
// falls back to the package default.
func (c *Ctx) logger() *slog.Logger {
	if c == nil || c.policy == nil {
		return slog.Default()
	}
	return c.policy.log
}

func newCtx() *Ctx {
	return &Ctx{
		initial:   map[string]any{},
		actions:   map[string]action{},
		dirty:     map[string]any{},
		hydrators: map[string]func(json.RawMessage) bool{},
	}
}

// dirtyAll gathers every signal this pass wrote, page-wide: a Set inside an
// child lands on that child's binder, so the root map alone would silently
// drop it.
func (c *Ctx) dirtyAll() map[string]any {
	if len(c.children) == 0 {
		return c.dirty
	}
	all := make(map[string]any, len(c.dirty))
	maps.Copy(all, c.dirty)
	for _, isl := range c.children {
		maps.Copy(all, isl.dirtyAll())
	}
	return all
}

// binderCtx adapts a Ctx to hcore.Binder so the binder plumbing stays off
// Ctx's public surface.
type binderCtx struct{ c *Ctx }

func (b binderCtx) DeclareSignal(slot string, initial any)              { b.c.declareSignal(slot, initial) }
func (b binderCtx) Hydrator(slot string, fn func(json.RawMessage) bool) { b.c.hydrator(slot, fn) }

// ctxOf unwraps the Ctx behind a renderer's binder; nil when the binder is not
// via's own (a bare h render).
func ctxOf(b hcore.Binder) *Ctx {
	if bc, ok := b.(binderCtx); ok {
		return bc.c
	}
	return nil
}

// signalSlot names the signal at field by its byte offset in the composition,
// so a signal's wire identity is independent of where the View renders it.
//
// Anything else panics. A Signal behind a pointer, slice, array, map or interface field,
// or bound off a value receiver's stack copy, has no field offset: its writes
// land on memory the render discards, and any invented name is positional, so
// a conditional Bind hands one signal's slot to another and the next post
// writes the wrong field. The subtraction is unsigned, so a field below the
// base wraps past size and fails the bound check along with one above it.
func (c *Ctx) signalSlot(field unsafe.Pointer) string {
	if base := c.unitV.base; base != nil && field != nil && c.unitV.sig != nil {
		if off := uintptr(field) - uintptr(base); off < c.unitV.size {
			if f, ok := c.unitV.sig.byOff[off]; ok {
				return f.wire(c.scopePrefix())
			}
		}
	}
	panic(hcore.Miswired("via: a rendered Signal has no slot in its unit — a child composition must be rendered " +
		"through via.Child, not by calling its View; and View needs a POINTER receiver with every " +
		"Signal (and every child) held as a plain struct field, not behind a pointer, slice, array, " +
		"map or interface"))
}

// csSeed returns field's declared start value — T's zero, or the JSON in a
// via:"…" tag — resolved once on the type walk (see signalsOf) because reflect
// cannot recover T from the field itself. Called only once signalSlot has
// already validated field's offset, so it never needs to panic on its own.
func (c *Ctx) csSeed(field unsafe.Pointer) any {
	off := uintptr(field) - uintptr(c.unitV.base)
	return c.unitV.sig.byOff[off].seed
}

// keyOf composes onto the parent's key, so a subtree re-rendered on its own
// (renderChildBind) numbers its descendants exactly as the full-page walk did.
func (c *Ctx) keyOf(ordinal int) string {
	if c.isChild {
		return c.childKey + "-" + strconv.Itoa(ordinal)
	}
	return strconv.Itoa(ordinal)
}

// scopePrefix lives on the instance so a live child's parentless push
// re-render reproduces it exactly — see instance.slotPrefix.
func (c *Ctx) scopePrefix() string { return c.unitV.slotPrefix }

// slotSet collects every slot this render declared, page-wide.
func (c *Ctx) slotSet() map[string]bool {
	seen := map[string]bool{}
	c.collectSlots(seen)
	return seen
}

func (c *Ctx) collectSlots(dst map[string]bool) {
	for _, slot := range c.order {
		dst[slot] = true
	}
	for _, isl := range c.children {
		isl.collectSlots(dst)
	}
}

// declareSignal records slot's initial value for the data-signals declaration.
// Idempotent within a render: the first call fixes the order, later ones (a
// Bind and a Display of one signal) only refresh the value. hcore.Binder.
func (c *Ctx) declareSignal(slot string, initial any) {
	if slot == tabSignal {
		// A user signal landing on this name would overwrite the CSRF token
		// the next action routes by (see tabSignal). Loud at the first render,
		// never a silent mid-session 410 storm.
		panic(hcore.Miswired("via: a signal named " + tabSignal + " collides with via's own tab-id signal — rename the field"))
	}
	if _, seen := c.initial[slot]; !seen {
		c.order = append(c.order, slot)
	}
	c.initial[slot] = initial
}

// hydrator records slot's update function. A live unit keeps the table from
// its last render (every push is a render), so a live action hydrates in place
// without re-rendering. hcore.Binder.
func (c *Ctx) hydrator(slot string, fn func(json.RawMessage) bool) {
	c.hydrators[slot] = fn
}

// PostForm renders a native <form method="post"> whose submit runs handler on
// the server — the flow for sign-up/in, file uploads, and anything ending in a
// Redirect. Unlike on.Submit, which element-patches in place, this is a real
// browser navigation: handler reads fields via ctx.Request().FormValue and may
// Redirect. The form is always multipart, so ctx.Request().FormFile works.
// handler is a named method value; children are the form contents.
//
// A native submit posts form fields, not the signal store, so in handler a
// Signal's Get returns its initial value, not what the input shows. To keep
// Signals on the form's inputs (for an on.Change that reshapes it, say), give
// each bound input an h.Name, read it with FormValue, and Set the Signal from
// it; the page the submit answers with renders those values. That page is
// rendered from the acted-on instance only for a plain unit: in a live unit
// the submit answers with a fresh page, and what handler set is gone.
//
// It carries a hidden _viatab field synced to the $viatab signal: a native
// submit carries neither Datastar's signal store nor its headers, so dispatch
// routes on this field instead — under the same per-mount ownership check.
func PostForm(handler func(*Ctx), children ...h.H) h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		ctx := ctxOf(r.Binder())
		if ctx == nil {
			return
		}
		idx := ctx.actionSlot(handler)
		r.WriteString(`<form method="post" enctype="multipart/form-data" action="` +
			hcore.EscapeString(actionPath(ctx, idx)) + `">`)
		r.WriteString(`<input type="hidden" name="` + tabFormField + `" data-attr:value="$` + tabSignal + `">`)
		for _, c := range children {
			r.Render(c)
		}
		r.WriteString(`</form>`)
	})
}

// paramMiss is the sentinel Param panics with when a segment cannot decode
// into the asked-for type; the transport recovers it into a 404. A
// control-flow sentinel, not an error value — user code never sees it.
type paramMiss struct {
	name string
	seg  string
}

// Param reads the mount pattern's named {name} segment, in http.ServeMux
// syntax (via.Mount(r, "/thread/{id}", …) → ctx.Param[int]("id")). Callable from
// OnInit, actions, and a live unit's Tick/Listen handlers; View is ctx-free,
// so load params in OnInit into a field instead.
//
// A segment that cannot decode into T answers 404 — never a silent zero value.
// Naming a segment the mount pattern doesn't have panics.
//
// In a [WithErrorPage] handler it returns the zero value instead, for every
// name: that Ctx is answering a failure that may have happened before any route
// matched, so there is no pattern to read a segment from — and an error page is
// the one render that must not be able to fail. Read [Ctx.Request] if the raw
// path matters there.
//
// Path params survive an action; query params do not. An action POSTs to
// {mount}/_via/a/{n}/…, built from the mount pattern with its {name} segments
// filled in — and nothing else. The page's "?q=urgent&sort=age" is not on that
// URL, so the discovery render that decides what is dispatchable runs against
// the unfiltered page: a row that only exists under the filter binds no action
// in that render, and clicking it answers 410. Ctx.Request().URL.Query() is
// therefore readable on the GET and empty on every action.
//
// So a page's list state — filter, page number, sort, tab — belongs in path
// params or in the session, never in the query string:
//
//	via.Mount(r, "/tickets/{status}/{page}", TicketList{}) // survives an action
//	// /tickets?status=open&page=2                    // does not
func (c *Ctx) Param[T any](name string) T {
	if c.errPage {
		var zero T
		return zero
	}
	seg := c.req.PathValue(name)
	if seg == "" && !strings.Contains(c.req.Pattern, "{"+name+"}") {
		panic("via: Param: the mount pattern has no {" + name + "} segment")
	}
	return decodeSegment[T](seg, name)
}

// decodeSegment turns a raw URL segment into T. Strings pass through verbatim
// (a path segment is not quoted JSON); everything else decodes as JSON, which
// parses "42" → 42 without a reflect-driven scalar table.
func decodeSegment[T any](seg string, name string) T {
	var v T
	if sp, ok := any(&v).(*string); ok {
		*sp = seg
		return v
	}
	if err := json.Unmarshal([]byte(seg), &v); err != nil {
		panic(paramMiss{name: name, seg: seg})
	}
	return v
}

// Redirect navigates the browser to path after the current handler returns,
// from anywhere: OnInit (before the View ever renders), OnReload, a PostForm
// submit, and a Datastar @post action — plain, embedded, or live. path must be
// relative or an absolute http(s) URL on this site: the request's own host, or
// an origin passed to WithTrustedOrigin. Anything else (another host, a
// javascript: or any other scheme) is dropped and logged, never followed, so a
// target read from the request cannot send the user off-site. Leave the site
// with RedirectExternal.
//
// The transport differs, the meaning does not: a full-page request answers 303,
// a @post answers the one-line navigation script Datastar executes (see
// redirectInit). A Redirect skips the unit's OnReload and the response render —
// nothing from this render is going to be shown.
func (c *Ctx) Redirect(path string) {
	c.redirect = redirectTo{url: path, refused: c.offSite(path)}
}

// RedirectExternal is Redirect to any http(s) URL, for a hand-off that has to
// leave the site (an OAuth provider, a payment page). The scheme gate still
// applies. Passing it a target read from the request unchecked is the open
// redirect Redirect refuses.
func (c *Ctx) RedirectExternal(target string) {
	r := redirectTo{url: target}
	if !hcore.SafeURL(target) {
		r.refused = notHTTP
	}
	c.redirect = r
}

// On wires a named DOM event (e.g. "click", "submit", "change") to a POST
// action. fn is a named method value (e.g. c.Inc) — pointer-bound to the
// via-owned instance, so no '&' at the call site. Datastar auto-prevents a
// wired form's default submit, so no prevent modifier is needed for "submit".
// It panics on an event name package on would refuse; Datastar modifiers
// ("click__debounce.500ms") are allowed.
//
// Deprecated: use package on (on.Click, on.Event, on.WithArg). Removed in v0.9.
func On(event string, fn func(*Ctx)) h.Attr {
	mustActionEvent("On", event)
	return hcore.DynAttr(func(r *hcore.Renderer) {
		ctx := ctxOf(r.Binder())
		if ctx == nil {
			return
		}
		// fn is stored as-is: dispatch calls it with a fresh per-dispatch Ctx,
		// never the one bound here at render time (see liveRunAction).
		idx := ctx.actionSlot(fn)
		writeActionAttr(r, ctx, event, idx, "")
	})
}

// OnArg wires a named DOM event to an action that carries a value: the row's
// own datum rides with the event, so the handler acts on that item regardless
// of its render position. fn is a named method value (e.g. l.Delete); arg is
// plain data (e.g. todo.ID), not an identifier string.
//
// DISPATCHABLE-IFF-RENDERED (this is the canonical statement of the property;
// via.When, Signal.bind and hydrateTree all defer to it). Dispatch identity is
// the (handler, arg) pair. The render that precedes every dispatch — the
// discovery render on a plain page, the last push on a live one — rebuilds the
// set of args it binds for fn, and a POST whose ?a= is not in that set is 410'd
// before fn ever runs. So an arg the caller's own render does not produce
// (another user's row, an id behind a When branch closed for them) is not
// dispatchable by them, and fn may treat its parameter as already authorized.
// Keep the render honest — filter the list by the caller's identity — and the
// handler needs no check of its own.
//
// That render is server state alone: an inbound value is accepted only for a
// slot the render put under client control (Bind), and the body is applied
// after the discovery render, so a POST cannot open the branch that authorizes
// it. A Bind()ed signal IS client-controlled, so gating on one is the client's
// decision to make — an authorization gate belongs on session or database
// state, never on a signal.
//
// trap: arg must be a stable identity (a row's primary key), never a value
// that moves between renders. A pagination cursor or count bound as an arg
// goes stale the moment any push re-renders the button, and the click already
// in flight 410s. Read changing state from the composition in an argless On
// handler instead.
//
// It panics on an event name On would refuse.
//
// Deprecated: use package on (on.Click, on.Event, on.WithArg). Removed in v0.9.
func OnArg[T any](event string, fn func(*Ctx, T), arg T) h.Attr {
	mustActionEvent("OnArg", event)
	return hcore.DynAttr(func(r *hcore.Renderer) {
		ctx := ctxOf(r.Binder())
		if ctx == nil {
			return
		}
		// Marshalled before the slot is claimed: the encoded arg is both what
		// the binding ships and what dispatch matches against, so one that
		// cannot encode has no authorizable identity — binding it anyway would
		// render a button whose every click 410s.
		data, err := json.Marshal(arg)
		if err != nil {
			ctx.logger().Warn("via: OnArg arg does not encode; the binding is dropped", "event", event, "err", err)
			return
		}
		idx := ctx.actionSlotArg(fn, func(rc *Ctx, name string, bound map[string]struct{}) {
			if rc.req == nil {
				return
			}
			var v T
			raw := rc.req.URL.Query().Get("a")
			if raw == "" || raw == "null" {
				panic(badActionArg{err: errors.New("missing action arg")})
			}
			if err := json.Unmarshal([]byte(raw), &v); err != nil {
				panic(badActionArg{err: err})
			}
			// The dispatchable-iff-rendered check (see OnArg). Matching the
			// exact bytes the binding shipped means a client echoing the URL it
			// was served passes, while any arg it invents — even a re-spelling
			// of a legitimate one — fails closed.
			if _, ok := bound[raw]; !ok {
				panic(unrenderedArg{name: name, raw: strconv.Quote(raw), have: len(bound)})
			}
			fn(rc, v)
		}, string(data))
		writeActionAttr(r, ctx, event, idx, "a="+url.QueryEscape(string(data)))
	})
}

// actionEvent is package on's event-name grammar followed by Datastar
// modifiers. writeActionAttr splices the name into an attribute name raw, so a
// quote, space or '=' would open an attribute of the caller's choosing.
var actionEvent = regexp.MustCompile(`^[a-z][a-z0-9]*(?:[:.-][a-z0-9]+)*(?:__[a-z]+(?:\.[a-z0-9]+)*)*$`)

func mustActionEvent(fn, event string) {
	if !actionEvent.MatchString(event) {
		panic("via: " + fn + " event name " + strconv.Quote(event) +
			` must be lower-case letters and digits, optionally joined by ':', '.' or '-', then Datastar modifiers (such as "click" or "input__debounce.250ms")`)
	}
}

// badActionArg is the sentinel a value-carrying slot panics with when ?a=
// fails to decode into T. The honest answer is 400, not silently handing the
// handler a zero value it might act on (deleting row 0).
type badActionArg struct{ err error }

// unrenderedArg is the sentinel for a ?a= that decodes fine but names a value
// this request's render never bound (→ 410). A sentinel and not a dispatch-site
// check because the distinction only exists once the arg is decoded into T:
// dispatch has no T, the slot's own closure does.
type unrenderedArg struct {
	name string
	raw  string
	have int
}

// body is the 410 response text. The diagnosis goes to the log, never the
// response: the bound set is other rows' identities, and handing it back
// answers the very question an arg-swapping client is asking.
func (u unrenderedArg) body(log *slog.Logger) string {
	log.Warn("via: the action is bound, but not for this arg",
		"act", u.name, "arg", u.raw, "bound", u.have)
	return "this render does not bind that action for that argument"
}

// writeActionAttr writes the data-on:<event>="@post('PATH')" binding for a
// claimed action slot. Written raw, not via h.Data: the value is a Datastar
// expression whose single quotes must survive verbatim, and every byte of it is
// via-generated or checked (fixed template, an event name On and OnArg held to
// actionEvent, hashed id, url-encoded arg), so no attribute-breaking byte
// reaches it. The colon spelling is
// mandatory — see h.Data.
//
// The query (?u= for a child, ?a= for an arg) goes in a data-via-q-<event>
// attribute the expression reads off el, so the expression text is one per
// action and event. Datastar in nonce mode compiles each distinct expression
// into a script it never evicts, so a query inline would leave one per row for
// the document's life. Datastar ignores data-* names with no plugin, and the
// event suffix keeps two bindings on one element apart.
//
// id addresses the handler itself, so a list that grew or shrank since the
// client's copy was rendered still routes every already-shipped URL. The tab
// id rides in the signal store Datastar already ships with every @post rather
// than in a header, because Datastar builds headers per call (Object.assign
// over that action's opts.headers) — no inheritance, no config hook, so a
// header would cost 33 bytes on every binding.
func writeActionAttr(r *hcore.Renderer, ctx *Ctx, event, idx, arg string) {
	path := "/_via/a/" + rootAddr + "/" + idx
	if ctx != nil {
		path = actionPath(ctx, idx) // mount prefix: a page at /profile posts to /profile/_via/a/{child}/{id}
	}
	path, query, _ := strings.Cut(path, "?")
	if arg != "" && query != "" {
		query += "&"
	}
	query += arg
	if query == "" {
		r.WriteString(` data-on:` + event + `="@post('` + hcore.EscapeString(path) + `')"`)
		return
	}
	qAttr := "data-via-q-" + event
	r.WriteString(` ` + qAttr + `="` + hcore.EscapeString("?"+query) + `" data-on:` + event + `="@post('` +
		hcore.EscapeString(path) + `' + (el.getAttribute('` + qAttr + `') ?? ''))"`)
}

// Handler builds a single-page app: a [Router] with root mounted at "/". root
// is taken by value; per request via copies it into an addressable local and
// operates on the pointer, so pointer-receiver methods work without '&' at the
// call site. The PT constraint makes a missing or mistyped View() a compile
// error rather than a first-request 500; Handler(Counter{}) still infers both
// parameters.
//
// Like Mount, it renders root once and panics on a wiring mistake that render
// reaches.
//
// It returns the *Router rather than an http.Handler so the live half is
// reachable: a via app owns goroutines, and [Router.Shutdown] (or
// [Router.Close], with no deadline) is what drains them.
//
//	r := via.Handler(Counter{})
//	defer r.Close()
//	http.ListenAndServe(":8080", r)
func Handler[T any, PT ptrViewer[T]](root T, opts ...Option) *Router {
	r := NewRouter(opts...)
	Mount[T, PT](r, "/", root)
	return r
}
