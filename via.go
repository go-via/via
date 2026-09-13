// Package via is a server-driven reactive UI toolkit built on the h DSL and the
// Datastar client: plain request/response pages, SSE-backed live embeds,
// server-authoritative State/List/Signal, and always-on sessions.
//
// Hard guarantees (the point of the design): no '&' at any user call site, no
// reflection in the public API surface (one internal reflect.ValueOf reads a
// handler's own func name to address its action), no closures in it either, no any in element/child
// signatures. The library is stdlib-only. Identifier strings do appear at the
// edges the caller controls directly — ctx.Param[T]("id"), FormFile("avatar"),
// Mount("/thread/{id}") — but never as an internal wire-name a caller could
// desync (see the Field-Embeddable Types convention).
package via

import (
	"bytes"
	"context"
	_ "embed"
	"errors"
	"io"
	"log"
	"maps"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"crypto/sha256"
	"encoding/base64"
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

// viewer is the (pointer) contract a root must satisfy: a pure, ctx-free View.
type viewer interface{ View() h.H }

// instance is one root/child composition plus the base address and size a
// Signal names itself by. Both come from the generic entry point (Mount's T,
// Embed's C); everything downstream is non-generic and would otherwise reflect.
type instance struct {
	v    viewer
	base unsafe.Pointer
	size uintptr
	typ  reflect.Type // the composition's struct type, for the field-name signal table
	sig  *typeSignals // that table, resolved once per type (never per render)
	// Carried on the instance, not derived from the embed key: a live embed's
	// push re-renders the child with no parent in scope and must still mint
	// the exact same names the full-page walk did.
	slotPrefix string
}

// slotID is embedded FIRST in Signal[T] so prebindSignals can stamp every
// field-held signal's name through one pointer add, not a reflect per render.
type slotID struct {
	slot string // stable wire name
}

func (*slotID) isViaSignal() {}

var signalMarker = reflect.TypeOf((*interface{ isViaSignal() })(nil)).Elem()

var viewerType = reflect.TypeOf((*viewer)(nil)).Elem()

// typeSignals is one composition type's signal table: every Signal-typed
// field's byte offset paired with the wire name its Go field path gives it.
type typeSignals struct {
	fields []signalField
	byOff  map[uintptr]string
	names  map[string]bool // every minted slot name, for the embed-prefix collision check
}

type signalField struct {
	off  uintptr
	name string
}

var typeSignalCache sync.Map // reflect.Type -> *typeSignals

// signalsOf builds (memoized per type) the offset -> wire-name table: the Go
// FIELD name, first rune lowercased, "_"-joined through plain nested structs.
//
// "_" and not "." is deliberate: Datastar treats a dotted signal name as a PATH
// and re-nests it, so "chat.draft" would arrive back as {"chat":{"draft":…}}
// and never match the flat slot the server looks up.
func signalsOf(t reflect.Type) *typeSignals {
	if v, ok := typeSignalCache.Load(t); ok {
		return v.(*typeSignals)
	}
	ts := &typeSignals{byOff: map[uintptr]string{}}
	minted := map[string]bool{}
	var walk func(t reflect.Type, base uintptr, prefix string, depth int)
	walk = func(t reflect.Type, base uintptr, prefix string, depth int) {
		if t == nil || t.Kind() != reflect.Struct || depth > 8 { // depth: a self-referential composition must not spin here
			return
		}
		for i := range t.NumField() {
			f := t.Field(i)
			name, off := prefix+lowerFirst(f.Name), base+f.Offset
			if reflect.PointerTo(f.Type).Implements(signalMarker) {
				checkSlotName(t, f.Name, name, minted)
				minted[name] = true
				ts.fields = append(ts.fields, signalField{off: off, name: name})
				ts.byOff[off] = name
				continue
			}
			if f.Type.Kind() == reflect.Struct {
				walk(f.Type, off, name+"_", depth+1)
				continue
			}
			checkSignalReachable(t, f)
		}
	}
	walk(t, 0, "", 0)
	ts.names = minted
	typeSignalCache.Store(t, ts)
	return ts
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
// here instead, on the type walk Mount and Embed do once.
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

// prebindSignals stamps every field-held Signal with its wire name BEFORE the
// View runs, so Ref reads a real name wherever it is called and an Embed's
// by-value copy carries the embed's prefix, not the parent's last binding.
func prebindSignals(c *Ctx, inst instance) {
	if inst.sig == nil || inst.base == nil {
		return
	}
	prefix := c.scopePrefix()
	for _, f := range inst.sig.fields {
		(*slotID)(unsafe.Add(inst.base, f.off)).slot = prefix + f.name
	}
}

// embedPrefixKey memoizes the parent-type -> child-type field-name lookup.
type embedPrefixKey struct{ parent, child reflect.Type }

var embedPrefixes sync.Map // embedPrefixKey -> string ("" = ambiguous)

// embedFieldName is the parent field an Embed's child came from. Embed takes
// the child BY VALUE, so the copy's address says nothing about which field it
// came from; the type does, as long as the parent holds exactly ONE field of
// it. Two fields of that type answer "" (Embed's argument order need not match
// declaration order), and the caller falls back to the positional embed key.
func embedFieldName(parent, child reflect.Type) string {
	if parent == nil || child == nil || parent.Kind() != reflect.Struct {
		return ""
	}
	k := embedPrefixKey{parent, child}
	if v, ok := embedPrefixes.Load(k); ok {
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
		// An embed scopes its child's slots under name+"__", while a plain
		// field literally named A__b mints "a__b" in the PARENT — same slot,
		// two different fields.
		for own := range signalsOf(parent).names {
			if strings.HasPrefix(own, name+"__") {
				panic("via: signal slot " + own + " on " + parent.String() +
					" collides with the embed prefix of field " + name + " — rename the field")
			}
		}
	}
	embedPrefixes.Store(k, name)
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
type Ctx struct {
	order       []string                         // slots in assignment order
	initial     map[string]any                   // per-slot value seen at render time
	actions     map[string]action                // content-addressed action table, keyed by handler identity
	hydrators   map[string]func(json.RawMessage) // per-slot updater, kept from the last render so a live action hydrates without re-rendering
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
	sessW       http.ResponseWriter // nil once the connect response is flushed, so a Tick/Listen Ctx's Session().Put warns instead of writing a dead response (I2)
	session     *Session
	embeds      []*Ctx // embedded children, in positional order (parent binder only)
	isEmbed     bool
	embedKey    string // ordinal among the parent's Embeds, composed with the parent's key ("0", "0-0")
	embedV      instance
	rendered    []byte // inner HTML from the discovery render (for the 204 compare)
	push        func() // re-render THIS embed and frame it on the stream
	declare     bool   // this render declares page-level data-signals
	base        string // mount path prefix for action POSTs
	redirect    string
	doInit      bool   // request-scoped, so every embedded child's OnInit runs before its View
	actedKey    string // embed key of the unit an action just mutated; Embed re-uses that instance instead of re-copying the parent's pristine field
	actedInst   instance
	initDone    bool       // a Tick/Listen after this would register into a snapshot nobody reads
	reinit      bool       // this Ctx is the post-action re-run of OnInit: load again, register nothing (I5)
	rev         *revertSet // live only: how to put the server-authored signal values back after a display render (see livePush)
}

// Request returns the HTTP request that triggered this handler — headers,
// cookies, URL, RemoteAddr, TLS. Nil when no request is in scope (a bare
// render).
//
// Read-only: the body is already consumed into the request's signals, and a
// live action runs on the stream goroutine after the POST has acked, so the
// request's Context may already be done.
func (c *Ctx) Request() *http.Request { return c.req }

// newCtx builds a Ctx with the given hydration map (may be nil for a GET page).
func newCtx() *Ctx {
	return &Ctx{
		initial:   map[string]any{},
		actions:   map[string]action{},
		dirty:     map[string]any{},
		hydrators: map[string]func(json.RawMessage){},
	}
}

// dirtyAll gathers every signal this pass wrote, page-wide: a Set inside an
// embed lands on that embed's binder, so the root map alone would silently
// drop it.
func (c *Ctx) dirtyAll() map[string]any {
	if len(c.embeds) == 0 {
		return c.dirty
	}
	all := make(map[string]any, len(c.dirty))
	maps.Copy(all, c.dirty)
	for _, isl := range c.embeds {
		maps.Copy(all, isl.dirtyAll())
	}
	return all
}

// binderCtx adapts a Ctx to hcore.Binder so the binder plumbing stays off
// Ctx's public surface.
type binderCtx struct{ c *Ctx }

func (b binderCtx) DeclareSignal(slot string, initial any)         { b.c.declareSignal(slot, initial) }
func (b binderCtx) Hydrator(slot string, fn func(json.RawMessage)) { b.c.hydrator(slot, fn) }

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
// writes the wrong field. The subtraction is unsigned, so a field BELOW the
// base wraps past size and fails the bound check along with one above it.
func (c *Ctx) signalSlot(field unsafe.Pointer) string {
	if base := c.embedV.base; base != nil && field != nil && c.embedV.sig != nil {
		if off := uintptr(field) - uintptr(base); off < c.embedV.size {
			if name, ok := c.embedV.sig.byOff[off]; ok {
				return c.scopePrefix() + name
			}
		}
	}
	panic("via: a rendered Signal is not a plain field of its composition — give View a POINTER " +
		"receiver, and hold every Signal (and every child composition) as a plain struct field, " +
		"not behind a pointer, slice, array, map or interface")
}

// childKey composes onto the parent's key, so a subtree re-rendered on its own
// (renderEmbedBind) numbers its descendants exactly as the full-page walk did.
func (c *Ctx) childKey(ordinal int) string {
	if c.isEmbed {
		return c.embedKey + "-" + strconv.Itoa(ordinal)
	}
	return strconv.Itoa(ordinal)
}

// scopePrefix lives on the instance so a live embed's parentless push
// re-render reproduces it exactly — see instance.slotPrefix.
func (c *Ctx) scopePrefix() string { return c.embedV.slotPrefix }

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
	for _, isl := range c.embeds {
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
		panic("via: a signal named " + tabSignal + " collides with via's own tab-id signal — rename the field")
	}
	if _, seen := c.initial[slot]; !seen {
		c.order = append(c.order, slot)
	}
	c.initial[slot] = initial
}

// action is one entry in a unit's action table.
type action struct {
	fn     func(*Ctx)
	name   string       // the Go name, so a 410 on a vanished handler reads as a name, not a bare id
	handle actionHandle // runtime identity, compared to catch an id collision
	// args is the set of ?a= payloads this render bound for the handler, in the
	// exact JSON the binding shipped. nil marks an argless action (On/PostForm).
	args map[string]struct{}
}

// actionSlot registers run under the content-addressed id of ident — for
// OnArg that is the user's fn, NOT the decoding wrapper, which would name the
// same via-internal closure for every row.
//
// The id hashes the func's Go NAME plus its receiver's byte offset, not its
// code pointer: the name survives a rebuild, so a deploy does not invalidate
// every action URL an open tab is holding, while the offset keeps two
// instances of one type (struct{ A, B Counter }) from minting one id for A.Inc
// and B.Inc. A receiver not addressable inside the composition (a closure, a
// child behind a pointer or slice field, a value receiver) has no offset and
// falls back to the name alone; two such handlers that hash alike would
// last-wins misroute, so claimAction panics instead.
func (c *Ctx) actionSlot(run func(*Ctx)) string {
	id, _, a := c.claimSlot(run)
	a.fn = run
	c.actions[id] = a
	return id
}

// actionSlotArg is actionSlot for a value-carrying action: it records arg in
// the slot's allowed set, so dispatch can reject a (handler, arg) pair this
// render never produced. The set is created once per slot and mutated in place
// as later rows claim it, so the closure stored here (the last row's) closes
// over the finished set no matter which row wrote it.
func (c *Ctx) actionSlotArg(ident any, run func(rc *Ctx, name string, bound map[string]struct{}), arg string) string {
	id, name, a := c.claimSlot(ident)
	if a.args == nil {
		a.args = map[string]struct{}{}
	}
	a.args[arg] = struct{}{}
	args := a.args
	a.fn = func(rc *Ctx) { run(rc, name, args) }
	c.actions[id] = a
	return id
}

// claimSlot resolves ident's action id and rejects a collision between two
// handlers via cannot tell apart, returning the id, the handler name, and the
// row to fill in (carrying any args a previous claim on this id recorded).
func (c *Ctx) claimSlot(ident any) (string, string, action) {
	id, name, ah := c.actionID(ident)
	prev, dup := c.actions[id]
	if dup && prev.handle != ah {
		panic("via: two different actions share the action id " + id + ": " + prev.name +
			" and " + name + " — their receivers are not addressable inside this unit " +
			"(a closure, or a child held through a pointer/slice field), so via cannot tell " +
			"them apart; give each child its own via.Embed embed")
	}
	return id, name, action{name: name, handle: ah, args: prev.args}
}

// actionHandle is a handler's runtime identity within ONE render: its code
// pointer plus the bound receiver (method value) or funcval. Never hashed into
// the id — both halves move every request — only compared, to turn a would-be
// silent last-wins overwrite into a panic.
type actionHandle struct {
	code uintptr
	self unsafe.Pointer
}

// eface is the runtime layout of an interface value; for a func in an any,
// data is the *funcval.
type eface struct{ typ, data unsafe.Pointer }

// funcSelf returns the func value's *funcval.
//
// A funcval is a code pointer followed by the captured words, and the compiler
// heap-allocates a method-value wrapper even for a zero-sized receiver — so
// the second word is in bounds for a "-fm" func and ONLY for one. A plain func
// or capture-free closure is a one-word static symbol; reading past it would
// be out of bounds, so those are identified by the pointer itself, never
// dereferenced.
func funcSelf(fn any) unsafe.Pointer { return (*eface)(unsafe.Pointer(&fn)).data }

// methodRecv reads the captured receiver. Valid only for a "-fm" func value —
// see funcSelf.
func methodRecv(self unsafe.Pointer) unsafe.Pointer {
	if self == nil {
		return nil
	}
	return *(*unsafe.Pointer)(unsafe.Add(self, unsafe.Sizeof(uintptr(0))))
}

// actionID content-addresses a handler by its Go name ("main.(*Poll).Vote-fm")
// plus, when the receiver lies inside this unit's composition, that receiver's
// byte offset — the name alone drops the receiver, so two instances of one
// type would collapse onto a single action.
func (c *Ctx) actionID(fn any) (id, name string, ah actionHandle) {
	pc := reflect.ValueOf(fn).Pointer()
	fnName := funcName(pc)
	name, isMethod := fnName.name, fnName.method
	self := funcSelf(fn)
	ah = actionHandle{code: pc, self: self}
	off, scoped := uintptr(0), false
	if isMethod {
		recv := methodRecv(self)
		ah.self = recv // two takes of the same method value are two funcvals; the receiver is the identity
		if base := c.embedV.base; base != nil && recv != nil {
			// Unsigned, so a receiver below the base wraps past size and fails
			// the bound check along with one above it.
			if o := uintptr(recv) - uintptr(base); o < c.embedV.size {
				off, scoped = o, true
			}
		}
	}
	return actionIDFor(pc, name, off, scoped), name, ah
}

// funcMeta is the per-code-pointer half of the actionID memo; both halves
// are fixed by the PC.
type funcMeta struct {
	name   string
	method bool
}

var funcNames sync.Map // uintptr (code pointer) -> funcMeta

// funcName resolves a code pointer's Go name once per process:
// runtime.FuncForPC walks the module's pclntab (~190ns with the sha256 below),
// which at a thousand bindings is a measurable slice of every render.
func funcName(pc uintptr) funcMeta {
	if v, ok := funcNames.Load(pc); ok {
		return v.(funcMeta)
	}
	info := funcMeta{name: "unknown"}
	if f := runtime.FuncForPC(pc); f != nil {
		info.name = f.Name()
	}
	info.method = strings.HasSuffix(info.name, "-fm")
	funcNames.Store(pc, info)
	return info
}

// actionIDKey is what an action id is a pure function of.
type actionIDKey struct {
	pc     uintptr
	off    uintptr
	scoped bool
}

var actionIDs sync.Map // actionIDKey -> string

func actionIDFor(pc uintptr, name string, off uintptr, scoped bool) string {
	k := actionIDKey{pc: pc, off: off, scoped: scoped}
	if v, ok := actionIDs.Load(k); ok {
		return v.(string)
	}
	key := name
	if scoped {
		key = name + "@" + strconv.FormatUint(uint64(off), 10)
	}
	sum := sha256.Sum256([]byte(key))
	id := base64.RawURLEncoding.EncodeToString(sum[:])[:8]
	actionIDs.Store(k, id)
	return id
}

// hydrator records slot's update function. A live unit keeps the table from
// its last render (every push is a render), so a live action hydrates in place
// without re-rendering. hcore.Binder.
func (c *Ctx) hydrator(slot string, fn func(json.RawMessage)) {
	c.hydrators[slot] = fn
}

// PostForm renders a native <form method="post"> whose submit runs handler on
// the server — the flow for sign-up/in, file uploads, and anything ending in a
// Redirect. Unlike On("submit", …), which element-patches in place, this is a
// real browser navigation: handler reads fields via ctx.Request().FormValue
// and may Redirect. The form is always multipart, so ctx.Request().FormFile
// works. handler is a named method value; children are the form contents.
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
			hcore.EscapeString(ctx.base) + `/_via/a/` + unitAddr(ctx) + `/` + idx + `">`)
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
// syntax (r.Mount("/thread/{id}", …) → ctx.Param[int]("id")). Callable from
// OnInit, actions, and a live unit's Tick/Listen handlers; View is ctx-free,
// so load params in OnInit into a field instead.
//
// A segment that cannot decode into T answers 404 — never a silent zero value.
// Naming a segment the mount pattern doesn't have panics.
func (c *Ctx) Param[T any](name string) T {
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
// http/https or a same-origin relative path; any other scheme is dropped and
// logged, never followed.
//
// The transport differs, the meaning does not: a full-page request answers 303,
// a @post answers the one-line navigation script Datastar executes (see
// redirectInit). A Redirect skips the unit's OnReload and the response render —
// nothing from this render is going to be shown.
func (c *Ctx) Redirect(path string) {
	c.redirect = path
}

// On wires a named DOM event (e.g. "click", "submit", "change") to a POST
// action. fn is a named method value (e.g. c.Inc) — pointer-bound to the
// via-owned instance, so no '&' at the call site. Datastar auto-prevents a
// wired form's default submit, so no prevent modifier is needed for "submit".
func On(event string, fn func(*Ctx)) h.Attr {
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
// own datum rides with the event, so the handler acts on THAT item regardless
// of its render POSITION. fn is a named method value (e.g. l.Delete); arg is
// plain data (e.g. todo.ID), not an identifier string.
//
// DISPATCHABLE-IFF-RENDERED (this is the canonical statement of the property;
// via.When, Signal.bind and hydrateTree all defer to it). Dispatch identity is
// the (handler, arg) PAIR. The render that precedes every dispatch — the
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
// AFTER the discovery render, so a POST cannot open the branch that authorizes
// it. A Bind()ed signal IS client-controlled, so gating on one is the client's
// decision to make — an authorization gate belongs on session or database
// state, never on a signal.
//
// TRAP: arg must be a stable IDENTITY (a row's primary key), never a value
// that moves between renders. A pagination cursor or count bound as an arg
// goes stale the moment any push re-renders the button, and the click already
// in flight 410s. Read changing state from the composition in an argless On
// handler instead.
func OnArg[T any](event string, fn func(*Ctx, T), arg T) h.Attr {
	return hcore.DynAttr(func(r *hcore.Renderer) {
		ctx := ctxOf(r.Binder())
		if ctx == nil {
			return
		}
		// Marshalled BEFORE the slot is claimed: the encoded arg is both what
		// the binding ships and what dispatch matches against, so one that
		// cannot encode has no authorizable identity — binding it anyway would
		// render a button whose every click 410s.
		data, err := json.Marshal(arg)
		if err != nil {
			log.Printf("via: OnArg(%q): arg does not encode (%v); the binding is dropped", event, err)
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
		writeActionAttr(r, ctx, event, idx, "?a="+url.QueryEscape(string(data)))
	})
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
func (u unrenderedArg) body() string {
	log.Printf("via: %s is bound, but not for arg %s; this render binds %d arg(s) for it",
		u.name, u.raw, u.have)
	return "this render does not bind that action for that argument"
}

// writeActionAttr writes the data-on:<event>="@post('PATH?query')" binding for
// a claimed action slot. Written raw, not via h.Data: the value is a Datastar
// expression whose single quotes must survive verbatim, and every byte of it is
// via-generated (fixed template, via-controlled event name, hashed id,
// url-encoded arg), so no user input reaches it. The colon spelling is
// mandatory — see h.Data.
//
// id addresses the handler itself, so a list that grew or shrank since the
// client's copy was rendered still routes every already-shipped URL. The tab
// id rides in the signal store Datastar already ships with every @post rather
// than in a header, because Datastar builds headers per CALL (Object.assign
// over that action's opts.headers) — no inheritance, no config hook, so a
// header would cost 33 bytes on every binding.
func writeActionAttr(r *hcore.Renderer, ctx *Ctx, event, idx, query string) {
	base := ""
	if ctx != nil {
		base = ctx.base // mount prefix: a page at /profile posts to /profile/_via/a/{embed}/{id}
	}
	path := base + "/_via/a/" + unitAddr(ctx) + "/" + idx
	r.WriteString(` data-on:` + event + `="@post('` + hcore.EscapeString(path+query) + `')"`)
}

// renderRootBase renders inst into <div id="root">…</div> and returns the bind
// Ctx plus the bytes. It never hydrates from request echoes: every render that
// reaches here follows an action, so the answer must reflect mutated server
// state.
//
// declareSignals false is what a LIVE push passes: re-declaring data-signals on
// every push would re-merge and clobber a signal the user is mid-edit (their
// half-typed message vanishing when someone else's arrives). Server-driven
// signal changes ride an explicit signal-patch instead. only restricts the
// declaration further, for a plain action's patch.
func renderRootBase(inst instance, declareSignals bool, base string, only map[string]any, seen map[string]bool, from *Ctx, rev *revertSet) (*Ctx, []byte) {
	ctx := newRootCtx(declareSignals, base, only)
	ctx.declareSeen = seen
	ctx.embedV = inst
	ctx.rev = rev
	inheritRequestScope(ctx, from)
	return ctx, renderRootWith(ctx, inst.v)
}

// inheritRequestScope carries a request-scoped render's wiring onto a LATER
// render off the same request (an action's response, a live root's push).
//
// doInit is load-bearing: the acted-on unit's OnInit already ran, but every
// nested child here is a fresh copy whose OnInit never has. Skipping them
// served the child zero-valued, nothing like what a GET renders.
func inheritRequestScope(ctx, from *Ctx) {
	if from == nil {
		return
	}
	ctx.doInit = true
	ctx.req = from.req
	ctx.sessions = from.sessions
	ctx.sessW = from.sessW
	// The resolved handle, not just the manager: a session minted THIS request
	// has its cookie on w and nothing in req, so a re-resolve here would miss it
	// and mint a second id (and a second Set-Cookie).
	ctx.session = from.session
	// An EMBED's mutation landed on the copy via.Embed made at the previous
	// render. A root walk from here would call via.Embed(parent.Field) again
	// and re-copy the parent's untouched field, throwing it away (validation
	// errors gone, submitted values back to empty) — so carry the instance down
	// for the walk to substitute at its own key. A root action mutates the very
	// instance the walk starts from, so it needs nothing.
	if from.isEmbed {
		ctx.actedKey, ctx.actedInst = from.embedKey, from.embedV
	}
}

// newRootCtx builds the root bind Ctx for one render. A request-scoped
// transport takes it before rendering so runOnInit registers the root's
// Tick/Listen on the very Ctx the render (and the liveness verdict) reads.
func newRootCtx(declareSignals bool, base string, only map[string]any) *Ctx {
	ctx := newCtx()
	ctx.declare = declareSignals // embeds declare their own signals only on a declaring render
	ctx.declareOnly = only
	ctx.base = base
	return ctx
}

// renderRootWith renders v under an already-built root Ctx. Liveness is an
// OUTPUT, not an input, so the nesting rules can only be checked once the
// whole walk is done — see checkLiveNesting.
func renderRootWith(ctx *Ctx, v viewer) []byte {
	declareSignals, only := ctx.declare, ctx.declareOnly
	prebindSignals(ctx, ctx.embedV)
	rr := hcore.NewRenderer(binderCtx{ctx})
	rr.Render(v.View())
	var b bytes.Buffer
	b.WriteString(`<div id="root"`)
	if declareSignals {
		writeSignalsAttr(&b, ctx.order, ctx.initial, only, ctx.declareSeen)
	}
	b.WriteString(`>`)
	b.Write(rr.Bytes())
	b.WriteString(`</div>`)
	out := b.Bytes()
	checkLiveNesting(ctx, false)
	return out
}

// checkLiveNesting enforces the two deferred-feature rules on the finished
// render tree: a live unit may not hold another live unit, and a live EMBED's
// View may not call Embed at all (a live ROOT may — its plain children are
// re-inited on every push, see rootPush). It runs after the walk because
// liveness is only knowable then — loud and early, rather than serving a page
// that silently misroutes an action.
func checkLiveNesting(c *Ctx, underLive bool) {
	if c.live {
		if underLive {
			panic("via: via.Embed: a live unit cannot sit inside another live unit — " +
				"embed it directly from a plain ancestor instead")
		}
		if c.isEmbed && len(c.embeds) > 0 {
			panic("via: via.Embed: a live embed's View must not call Embed — keep a live embed's View flat")
		}
	}
	for _, ch := range c.embeds {
		checkLiveNesting(ch, underLive || c.live)
	}
}

// Handler builds an http.Handler serving the root composition. root is taken
// by value; per request via copies it into an addressable local and operates on
// the pointer, so pointer-receiver methods work without '&' at the call site.
// The PT constraint makes a missing or mistyped View() a compile error rather
// than a first-request 500; Handler(Counter{}) still infers both parameters.
func Handler[T any, PT ptrViewer[T]](root T, opts ...Option) http.Handler {
	r := NewRouter(opts...)
	r.Mount[T, PT]("/", root)
	return r
}

// liveUnits collects every live unit in bind's render, at any depth — the root
// first when live, then its live embeds in render order.
func liveUnits(bind *Ctx) []*Ctx {
	var units []*Ctx
	if bind.live {
		units = append(units, bind)
	}
	appendLiveEmbeds(bind, &units)
	return units
}

// appendLiveEmbeds recurses: a live grandchild gets no stream wiring unless
// discovery descends past its (live or plain) parent too.
func appendLiveEmbeds(ctx *Ctx, out *[]*Ctx) {
	for _, isl := range ctx.embeds {
		if isl.live {
			*out = append(*out, isl)
		}
		appendLiveEmbeds(isl, out)
	}
}

// connectUnit wires unit's push closure to stream (whole-page at #root for the
// root, its own #via-i{key} container for an embed) and registers it on lc.
func connectUnit(unit *Ctx, stream *stream, base string, lc *tabStream) {
	lc.replace(unit)
	if unit.isEmbed {
		unit.push = embedPush(unit.embedKey, unit.embedV, base, stream, lc)
	} else {
		unit.push = rootPush(unit.embedV, base, stream, lc, unit)
	}
}

// rootPush renders inst fresh and pushes the whole-page element-patch. The
// fresh bind Ctx replaces lc's entry, so a live action needs no render of its
// own — the previous push already built its actions/hydrators table.
//
// from is what makes a live root's plain EMBEDS survive a push: Embed re-copies
// each child from the root's field every render, so a child whose fields OnInit
// filled comes back zero-valued unless that OnInit runs again. COST: a plain
// child of a LIVE root runs its OnInit once per pushed frame — keep it cheap,
// or hold the data on the live root. A live EMBED may not Embed at all
// (checkLiveNesting), so embedPush needs none of this.
// livePush renders one live unit under the same two-phase rule dispatchPlain
// has always used (I1/I2) and the live path had no version of at all: the
// AUTHORITY render is the one the client's posted signals did not touch, and it
// alone decides what is dispatchable.
//
// Phase 1 puts the server-authored signal values back — undoing the previous
// cycle's application of lc.client, which otherwise became the server's own
// state, because hydration writes straight into a field of an instance that
// outlives the request — and renders. Phase 2 applies the client's signals to
// the slots that render made writable and re-renders to fixpoint, exactly as
// the plain path does, so a Bind()ed signal still steers what the client SEES
// and a branch it opens still gets the slots inside it hydrated. The frame
// carries phase 2; the table the connection keeps is phase 2 pruned to phase 1,
// per handler AND per arg.
//
// The authority render runs FIRST and the display render LAST because
// Signal.bind stamps the dirty sink at render time: the Ctx registered on the
// connection must be the one a later Set writes through.
//
// Cost: TWO renders per push on any page carrying a Bind() — a real Datastar
// connect body is the whole signal store, so lc.client holds every bindable
// slot from the first connect on. Each extra render re-runs every plain child's
// OnInit (inheritRequestScope sets doInit) and re-walks checkLiveNesting.
func livePush(lc *tabStream, render func(*revertSet) (*Ctx, []byte)) (*Ctx, []byte) {
	// ONE set for the life of the connection, never replaced: a hydrator closure
	// notes its undo into the rev of the Ctx that bound the signal, so a second
	// live unit's push swapping lc.rev would leave that unit pointing at a set
	// nothing restores — and its next action's client value would survive into
	// the AUTHORITY render. restore() clears the map, so reuse is not growth:
	// it is keyed by slot and bounded by the page's signal count.
	rev := lc.rev
	rev.restore()
	auth, body := render(rev)
	bind := auth
	done := map[string]bool{}
	n := 0
	for ; n < maxHydratePasses && hydrateTree(bind, lc.client, done); n++ {
		bind, body = render(rev)
	}
	if n == maxHydratePasses {
		log.Printf("via: live push hit the %d-pass hydration cap; some posted signals may be unapplied", maxHydratePasses)
	}
	if bind != auth {
		pruneToAuthority(bind, auth)
	}
	// The display render's client values are undone HERE, not just before the
	// next authority render: between pushes a Tick/Listen handler reads the
	// instance directly, and leaving the client's values on it hands attacker
	// data to server-side handler code. body is already built, so the frame the
	// client sees is unaffected.
	rev.restore()
	return bind, body
}

func rootPush(inst instance, base string, stream *stream, lc *tabStream, from *Ctx) func() {
	var push func()
	var initFailed bool
	push = func() {
		// A plain child's failed OnInit panics initOutcome from INSIDE this
		// render, on every frame, and a push has no response to turn that into
		// a 500/303/404. Dropping the frame loops forever with no
		// client-visible signal at all, and rendering the child empty serves a
		// page that is quietly wrong — so tear the stream down instead: the
		// client's reconnect re-requests the page and gets the real answer on a
		// path that can give one.
		defer func() {
			rec := recover()
			if rec == nil {
				return
			}
			ci, ok := rec.(initOutcome)
			if !ok {
				panic(rec)
			}
			if !initFailed {
				initFailed = true
				log.Printf("via: live push aborted — an embedded child's OnInit failed (err=%v redirect=%q); tearing the stream down so the client reconnects and gets the real answer", ci.err, ci.redirect)
			}
			stream.abort()
		}()
		bind, body := livePush(lc, func(rev *revertSet) (*Ctx, []byte) {
			return renderRootBase(inst, false, base, nil, nil, from, rev) // push omits data-signals
		})
		bind.push = push
		lc.replace(bind)
		stream.frame(func(w io.Writer) { writePatchFrame(w, body) })
	}
	return push
}

// embedPush is rootPush for a live embed: it re-renders at key in Datastar
// inner mode, so the container's own data-ignore-morph never blocks the push.
func embedPush(key string, inst instance, base string, stream *stream, lc *tabStream) func() {
	var push func()
	push = func() {
		bind, body := livePush(lc, func(rev *revertSet) (*Ctx, []byte) {
			return renderEmbedBind(key, inst, base, nil, rev)
		})
		bind.push = push
		lc.replace(bind)
		id := "via-i" + key
		stream.frame(func(w io.Writer) { writeInnerPatchFrame(w, id, body) })
	}
	return push
}

// connect is the SSE stream's entry point at {base}/_via/sse. Every mount gets
// the route; a page with no live content answers 404 on it.
func (m *mount) connect(w http.ResponseWriter, req *http.Request) {
	// Origin floor first: the stream opens a long-lived goroutine + timers, so
	// reject an unprovable source before allocating any of it.
	if !originAllowed(req, m.cfg) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	// A POST so the connect can carry the page's signals as a body.
	connectSig, ok := decodeSignals(w, req, modeDatastar)
	if !ok {
		return
	}
	if _, ok := w.(http.Flusher); !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	// Increment-then-check so the gauge can't be raced past the limit.
	if m.liveCount.Add(1) > int64(m.maxLive) {
		m.liveCount.Add(-1)
		http.Error(w, "stream capacity reached", http.StatusServiceUnavailable)
		return
	}
	defer m.liveCount.Add(-1)
	headersSent := false
	defer func() {
		if rec := recover(); rec != nil {
			// A panic before the headers went out would otherwise fall through
			// to Go's default — 200 with an empty body, telling the client the
			// connect succeeded. Once headers ARE sent this can't help, so a
			// mid-stream panic is caught per push item (runPushItem) instead.
			if !headersSent {
				recoverToHTTP(w, req, rec, "stream")
			}
		}
	}()
	pv := m.newInst()
	base := concreteBase(m.patternBase, req, m.names)
	// A half-open peer never cancels req.Context(); a failed frame write is the
	// only signal it's gone, so the stream needs a context it can cancel.
	streamCtx, cancel := context.WithCancel(req.Context())
	defer cancel()
	stream := &stream{
		w:       w,
		rc:      http.NewResponseController(w),
		timeout: sseWriteTimeout,
		cancel:  cancel,
	}
	keepalive := func() { stream.frame(writeKeepaliveFrame) }
	id := randomToken() // per-connection tab id (echoed as the viatab signal on actions)
	pushq := make(chan func())

	// The credential dispatch requires a match against, so that a leaked tab id
	// is not on its own a bearer token good from any origin with no session at
	// all (see dispatch). Resolved BEFORE OnInit, which may mint or rotate one:
	// the point is the identity the browser held when it opened this stream.
	// An error here is NOT "anonymous": the browser may well hold a valid
	// cookie the store just could not answer for. Opening the stream anyway
	// would leave it permanently unbound — nothing binds it later, since a
	// live action sees a session that already existed at connect — and the tab
	// id would be a bearer credential for the connection's whole life. Fail the
	// connect instead; the client's reconnect logic retries.
	_, sessDat, err := m.sessions.resolve(req)
	if err != nil {
		http.Error(w, "session store unavailable", http.StatusServiceUnavailable)
		return
	}

	// OnInit runs on the very Ctx the discovery render then binds, so a
	// Tick/Listen it registers is what makes the root a live unit.
	//
	// UN-HYDRATED, unlike every earlier version of this line: the connect body
	// is the client's, and this render is what decides the connection's action
	// table and its liveness (I1). The body is kept as lc.client and applied to
	// the DISPLAY render of every push instead (livePush) — which is the only
	// place it was ever visible, since connect frames no elements of its own.
	rev := newRevertSet()
	bind := newRootCtx(false, base, nil)
	bind.rev = rev
	bind.embedV = pv
	if runOnInit(pv.v, bind, w, req, m.sessions) != nil {
		return
	}
	renderRootWith(bind, pv.v)
	units := liveUnits(bind)

	if len(units) == 0 {
		http.Error(w, "no stream for this tab", http.StatusNotFound)
		return
	}

	var connSID string
	if sessDat != nil {
		connSID = sessDat.sid
	}

	// Built before the connect loop so each push closure can register itself as
	// the current unit on every render — a live action always runs against the
	// last render's actions/hydrators.
	lc := &tabStream{
		mount:       m,
		pushq:       pushq,
		done:        streamCtx.Done(),
		pushSignals: func(j string) { stream.frame(func(w io.Writer) { writeSignalsFrame(w, j) }) },
		units:       map[string]*Ctx{},
		sess:        connSID,
		client:      connectSig,
		rev:         rev,
	}

	// runStream owns the disposer sweep but is not running yet: an OnConnect fn
	// that panics before it takes over would strand every acquire already made
	// (a room joined with no matching part, for the life of the process). Armed
	// here and handed over at the call, and deferred AFTER the recover above so
	// it runs first.
	streaming := false
	defer func() {
		if streaming {
			return
		}
		for _, u := range units {
			for _, d := range u.disposers {
				runPushItem(d)
			}
		}
	}()

	for _, u := range units {
		connectUnit(u, stream, base, lc)
		for _, fn := range u.onConnect {
			fn()
		}
		// OnInit may have minted a session where the connect cookie left
		// lc.sess empty — bind it now so the connection isn't left as a
		// bare-tab-id credential (H1).
		lc.bindSession(u.session.sid())
	}

	m.reg.put(id, lc) // a live action POST routes to this connection by tab id
	defer m.reg.del(id)

	writeSSEHeaders(w)
	w.WriteHeader(http.StatusOK)
	headersSent = true
	stream.frame(func(w io.Writer) { writeSignalsFrame(w, `{"`+tabSignal+`":"`+id+`"}`) })

	// The connect response is flushed now. A Tick/Listen handler runs against
	// this SAME unit Ctx for the life of the connection, so leaving sessW set
	// would let its Session().Put SetCookie on a dead response: no error, no
	// cookie, a session minted and orphaned until TTL (I2). Clearing it makes
	// Session.ensure's "no cookie can be set" warning fire, as documented.
	for _, u := range units {
		u.sessW = nil
		if u.session != nil {
			u.session.w = nil // the handle cached w at resolve time; clearing the Ctx alone left it live
		}
	}

	streaming = true
	runStream(streamCtx, units, pushq, keepalive, sseHeartbeat)
}
