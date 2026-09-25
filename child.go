package via

import (
	"bytes"
	"log/slog"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"unsafe"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// Child renders a child composition — a plain struct field of the parent,
// seeded at the parent's literal — into its own positional container. The child
// gets its own OnInit (before its View, on every request-scoped render) and may
// be plain or live; liveness is what the child does, never a separate type. One
// live child anywhere makes the page stream.
//
//	type Page struct{ Chat ChatRoom; Ticker Clock }
//	func (p *Page) View() h.H { return h.Div(via.Child(p.Chat), via.Child(p.Ticker)) }
//
// The child is taken by value: value state stays isolated per connection while
// pointer deps (a shared room) are intentionally shared. The argument must be a
// field selector (p.Chat) — a composite literal would re-seed on every render.
// An optional region is via.When, not an empty child; a generic layout
// (type Shell[C any] struct{ Body C }) composes for free.
//
// The liveness rule:
//
//	A page streams iff it contains a live unit; a live child may sit under any
//	plain ancestor; a live unit may not contain another live unit.
//
// So nesting is open — nest live children as deep as you like, as long as every
// ancestor on the path is plain. Only the last clause limits: a live child's own
// View may not call Child either. Violations panic at render.
//
// trap: a Child's key is its position among its parent's Child calls, composed
// with the parent's ("0", "0-0"), and the container id, signal prefix and
// dispatch address all read it. A child's key must be identical on every render
// for the life of a connection, and a When wrapped around a Child shifts its
// later siblings' ordinals — so such a When may depend only on data fixed by
// OnInit or the field literal, never on time, a client signal, or shared state
// that changes while the page is open. A stale action URL whose key now holds
// a child of another type answers 410. One of the same type answers 410 when
// the two were reached through different calls during the render: separate
// via.Child expressions in a View or a When branch, or one helper called from
// separate places there. Otherwise it still runs on the child now at the key:
// one expression that renders either copy (a loop, a closure built per copy,
// c := p.A; if swap { c = p.B }; via.Child(c)), and any Child built outside
// the render, such as markup OnInit or OnReload stores in a field. A row is
// markup with on.WithArg, not a Child.
//
// It panics if the child has no View() method.
func Child[C any](child C) h.H {
	v, isView := any(&child).(viewer)
	if !isView {
		panic("via: via.Child(child) requires child to have a View() method")
	}
	// &child, not the parent's field: the copy is what the child's View binds
	// against for the life of this render (and, for a live child, for the life
	// of the connection), so its address is the base its signals offset from.
	typ := reflect.TypeOf(child)
	checkViewReceiver(typ)
	// slog.Default(): no Router in scope.
	checkHooks(slog.Default(), typ, &childHookWarned, false)
	inst := instance{v: v, base: unsafe.Pointer(&child), size: unsafe.Sizeof(child), typ: typ, sig: signalsOf(typ)}
	var site childSite
	runtime.Callers(2, site[:])
	return hcore.Dyn(func(r *hcore.Renderer) { childViewer(r, inst, site) })
}

// childSite is the call stack above a via.Child call. It is what tells apart
// two children the parent holds in fields of one type: the copy Child gets
// carries no trace of its field, but the calls that rendered each one differ.
type childSite [8]uintptr

var (
	childSiteIDs sync.Map // childSite -> string
	viaPkg       = reflect.TypeOf(instance{}).PkgPath() + "."
	hcorePkg     = reflect.TypeOf((*hcore.Renderer)(nil)).Elem().PkgPath() + "."
)

// id hashes the user frames of the site, from the Child call up to the first
// frame inside via: the frames above that differ between a GET, an action
// and a push, and must not change the id. Names and offsets into them rather
// than raw PCs, so a rebuild of unchanged code keeps every shipped action URL
// valid; an edit to a function that renders such a child 410s the URLs open
// tabs hold for it, once.
//
// Only a Child called while rendering (from a View, or a When branch) gets a
// site of its own. One built elsewhere and rendered later, such as markup a
// load helper builds in both OnInit and OnReload, is reached through
// different frames on the GET and on the action's re-render, so its site
// would change under a click that is still valid. Those all share the empty
// site: no worse than a positional key, and never a false 410.
func (s childSite) id() string {
	if v, ok := childSiteIDs.Load(s); ok {
		return v.(string)
	}
	n := 0
	for n < len(s) && s[n] != 0 {
		n++
	}
	var key strings.Builder
	rendering := false
	frames := runtime.CallersFrames(s[:n])
	for {
		f, more := frames.Next()
		if strings.HasPrefix(f.Function, viaPkg) || strings.HasPrefix(f.Function, hcorePkg) {
			rendering = renderCaller(f.Function)
			break
		}
		// The offset, not just the line: via.Child(p.A), via.Child(p.B) on one
		// line are two sites.
		key.WriteString(f.Function + "+" + strconv.FormatUint(uint64(f.PC-f.Entry), 10) + "\n")
		if !more {
			break
		}
	}
	id := ""
	if rendering {
		id = hashID(key.String())
	}
	childSiteIDs.Store(s, id)
	return id
}

// renderCaller reports whether fn is a via frame that calls user code during
// a render: the two View callers and via.When's branch. A rename here that
// this list misses degrades to the shared site, which the twin tests catch.
func renderCaller(fn string) bool {
	for _, name := range []string{"renderRootWith", "renderChildInner", "When."} {
		if strings.HasPrefix(fn, viaPkg+name) {
			return true
		}
	}
	return false
}

// childViewer renders the child into <div id="via-i{key}">, binds its
// signals/actions into a child-scoped Ctx, and appends it to the parent's
// children so a push or action patches exactly this one. A non-Ctx binder is a
// bare render with no parent to attach to, so it writes nothing.
func childViewer(r *hcore.Renderer, inst instance, site childSite) {
	parent := ctxOf(r.Binder())
	if parent == nil {
		return
	}

	key := parent.keyOf(len(parent.children))
	// The key is positional, so a When that closes ahead of a child moves a
	// sibling onto its key, and only the type guards below catch that. A
	// child named by its field is the one field of its type, so a matching
	// type is the same child. Two fields of one type need more: the render's
	// call stack into via.Child (see childSite.id), composed down the tree
	// because a shifted parent moves its children too. Child takes a copy, so
	// the field itself is out of reach.
	// It rides on every action URL the child renders (see actionPath).
	name := childFieldName(parent.unitV.typ, inst.typ)
	ident := parent.unitV.ident
	if name == "" {
		ident = hashID(ident + "/" + site.id())
	}

	// An action's response re-render substitutes the instance the handler
	// mutated for this fresh copy (see inheritRequestScope); re-running its
	// OnInit would undo the very change the handler just made — re-reading
	// mutated data is OnReload's job, and dispatch has already run it.
	//
	// The type guard is not paranoia: a root View whose Child order shifts
	// between the discovery render and the response re-render leaves the acted
	// key pointing at a slot a different type now occupies, and splicing the
	// instance in there renders the wrong View under the wrong slot prefix.
	// Fall back to the fresh copy rather than render a lie.
	acted := parent.actedKey != "" && parent.actedKey == key && inst.typ == parent.actedInst.typ &&
		ident == parent.actedInst.ident
	if acted {
		inst = parent.actedInst
	}
	// Same substitution for a hydration pass >= 2, and the same type guard. Its
	// OnInit already ran on this copy in the first pass; running it again would
	// overwrite what the pass before hydrated.
	var prior *Ctx
	if !acted && parent.passUnits != nil {
		if p, ok := parent.passUnits[key]; ok && p.unitV.typ == inst.typ && p.unitV.ident == ident {
			prior, inst = p, p.unitV
		}
	}

	child := newCtx()
	child.rev = parent.rev                         // a hydrated child must be revertable with its parent (see livePush)
	child.badDecodeLogged = parent.badDecodeLogged // one flag per tree: a malformed post hits every child's hydrator
	child.actedKey, child.actedInst = parent.actedKey, parent.actedInst
	child.passUnits = parent.passUnits
	if prior != nil {
		// That OnInit is also what registered the Tick or Listen making the unit
		// live, and a later pass may not lower the first pass's verdict (I5).
		child.live = prior.live
	} else if parent.passUnits != nil {
		parent.passUnits[key] = child
	}
	child.isChild = true
	child.childKey = key
	// The double underscore marks the scope boundary: a plain nested struct
	// joins with a single one, so a parent that binds p.C.S in its own View
	// (slot "c_s") and also embeds p.C (slot "c__s") keeps the two apart —
	// different copies that must never share a slot. Stamped onto the instance
	// because a live child's push re-renders with no parent in scope.
	inst.ident = ident
	if name != "" {
		inst.slotPrefix = parent.scopePrefix() + name + "__"
	} else {
		// Ambiguous field: fall back to the positional key, always right if
		// opaque. Its depth separator '-' is not a JS identifier character and
		// Ref() hands these names straight to Datastar expressions, so it
		// becomes '_' here. Only the slot name is respelled; the key still
		// addresses the container and the dispatch URL with '-'.
		inst.slotPrefix = parent.scopePrefix() + "i" + strings.ReplaceAll(key, "-", "_") + "__"
	}
	child.unitV = inst
	child.base = parent.base // the mount prefix, so the child's own action URLs carry it too
	child.declareSeen = parent.declareSeen
	child.req = parent.req
	child.sessions = parent.sessions
	child.sessW = parent.sessW
	child.session = parent.session // one resolved session per request tree — see inheritRequestScope
	parent.children = append(parent.children, child)

	// Only a request-scoped render inits: a live push re-renders the whole tree
	// every tick, and re-running a child's OnInit there would reload its data —
	// and re-register its Tick/Listen — once per beat.
	if parent.doInit {
		child.doInit = true
		if !acted && prior == nil {
			prebindSignals(child, inst)
			initChild(child, inst.v)
		}
	}

	// Render first: it is what populates the child's signal slots and settles
	// child.live, so neither the declaration nor the container attribute below
	// can be decided before it.
	child.rendered = renderChildInner(child, inst.v)
	r.WriteString(`<div id="via-i` + key + `"`)
	if child.live {
		// Datastar only skips a morph when both the existing element and the
		// incoming fragment carry the attribute, so every root-walk render must
		// mark it. The child's own push targets #via-i{key} in inner mode,
		// which never compares the container, so that push still lands.
		r.WriteString(` data-ignore-morph`)
	}
	if parent.declare && len(child.order) > 0 {
		var buf bytes.Buffer
		writeSignalsAttr(parent.logger(), &buf, child.order, child.initial, parent.declareOnly, parent.declareSeen)
		r.WriteString(buf.String())
	}
	r.WriteString(`>`)
	r.WriteString(string(child.rendered))
	r.WriteString(`</div>`)
}

// initOutcome is the sentinel initChild panics with: the render is already deep
// inside the parent's View with no return path, so the transport's recover
// turns it back into the answer OnInit asked for (see recoverToHTTP).
type initOutcome struct {
	err      error
	redirect string
}

// initChild runs an embedded child's OnInit before its View. A paramMiss panic
// propagates untouched — the transport already answers it 404.
func initChild(child *Ctx, v any) {
	startTracks(child, child.unitV)
	ic, ok := v.(initer)
	if !ok {
		return
	}
	defer func() { child.inInit = false }() // see runOnInit
	child.inInit = true
	err := ic.OnInit(child)
	if err != nil {
		panic(initOutcome{err: err})
	}
	if child.redirect != "" {
		panic(initOutcome{redirect: child.redirect})
	}
}

// renderChildInner renders the child's View with child as the binder, so its
// actions and signals bind into its own tables. Returns the inner HTML only.
func renderChildInner(child *Ctx, v viewer) []byte {
	prebindSignals(child, child.unitV)
	child.viewRan = true
	rr := hcore.NewRenderer(binderCtx{child})
	rr.Render(v.View())
	return rr.Bytes()
}

// renderChildBind re-renders the child at key (no hydration, so it reflects
// post-action state) and returns its bind Ctx alongside the inner HTML. The Ctx
// is what lets a plain action's response ship a Signal.Set it wrote. Seeding it
// with the child's own key keeps a plain child's own Child calls numbered
// exactly as the full-page walk numbered them, instead of restarting at the
// root's key and aliasing onto it.
//
// from is the request-scoped Ctx this re-render belongs to (nil for a live
// push); it inits the child's nested children — see inheritRequestScope.
//
// underLive says whether c itself sits under a live ancestor in the real
// tree — false for a live child's own push (its ancestor chain was already
// proven plain, or it could never have become live), and whatever the
// discovery render found for a plain action's re-render, since that unit may
// legitimately sit beneath an already-live root (see checkLiveUnderLive).
func renderChildBind(key string, inst instance, base string, from *Ctx, rev *revertSet, badDecodeLogged *atomic.Bool, underLive bool) (*Ctx, []byte) {
	c := newCtx()
	c.rev = rev
	c.badDecodeLogged = badDecodeLogged
	c.isChild = true
	c.childKey = key
	c.unitV = inst
	c.base = base
	inheritRequestScope(c, from)
	body := renderChildInner(c, inst.v)
	checkLiveUnderLive(c, underLive)
	return c, body
}
