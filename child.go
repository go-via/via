package via

import (
	"bytes"
	"reflect"
	"strings"
	"unsafe"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// Child renders a child composition — a plain struct field of the parent,
// seeded at the parent's literal — into its own positional container. The child
// gets its own OnInit (before its View, on every request-scoped render) and may
// be plain or live; liveness is what the child DOES, never a separate type. One
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
// TRAP: a Child's key is its POSITION among its parent's Child calls, composed
// with the parent's ("0", "0-0"), and the container id, signal prefix and
// dispatch address all read it. A child's key must be identical on every render
// for the life of a connection, and a When wrapped around a Child shifts its
// later siblings' ordinals — so such a When may depend only on data fixed by
// OnInit or the field literal, never on time, a client signal, or shared state
// that changes while the page is open.
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
	checkHooks(typ, &childHookWarned, false)
	inst := instance{v: v, base: unsafe.Pointer(&child), size: unsafe.Sizeof(child), typ: typ, sig: signalsOf(typ)}
	return hcore.Dyn(func(r *hcore.Renderer) { childViewer(r, inst) })
}

// childViewer renders the child into <div id="via-i{key}">, binds its
// signals/actions into a child-scoped Ctx, and appends it to the parent's
// children so a push or action patches exactly this one. A non-Ctx binder is a
// bare render with no parent to attach to, so it writes nothing.
func childViewer(r *hcore.Renderer, inst instance) {
	parent := ctxOf(r.Binder())
	if parent == nil {
		return
	}

	key := parent.keyOf(len(parent.children))

	// An action's response re-render substitutes the instance the handler
	// mutated for this fresh copy (see inheritRequestScope); re-running its
	// OnInit would undo the very change the handler just made — re-reading
	// mutated data is OnReload's job, and dispatch has already run it.
	//
	// The type guard is not paranoia: a root View whose Child ORDER shifts
	// between the discovery render and the response re-render leaves the acted
	// key pointing at a slot a DIFFERENT type now occupies, and splicing the
	// instance in there renders the wrong View under the wrong slot prefix.
	// Fall back to the fresh copy rather than render a lie.
	acted := parent.actedKey != "" && parent.actedKey == key && inst.typ == parent.actedInst.typ
	if acted {
		inst = parent.actedInst
	}

	child := newCtx()
	child.rev = parent.rev // a hydrated child must be revertable with its parent (see livePush)
	child.actedKey, child.actedInst = parent.actedKey, parent.actedInst
	child.isChild = true
	child.childKey = key
	// The DOUBLE underscore marks the scope boundary: a plain nested struct
	// joins with a single one, so a parent that binds p.C.S in its own View
	// (slot "c_s") and also embeds p.C (slot "c__s") keeps the two apart —
	// different copies that must never share a slot. Stamped onto the instance
	// because a live child's push re-renders with no parent in scope.
	if name := childFieldName(parent.unitV.typ, inst.typ); name != "" {
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
		if !acted {
			initChild(child, inst.v)
		}
	}

	// Render first: it is what populates the child's signal slots AND settles
	// child.live, so neither the declaration nor the container attribute below
	// can be decided before it.
	child.rendered = renderChildInner(child, inst.v)
	r.WriteString(`<div id="via-i` + key + `"`)
	if child.live {
		// Datastar only skips a morph when BOTH the existing element and the
		// incoming fragment carry the attribute, so every root-walk render must
		// mark it. The child's own push targets #via-i{key} in inner mode,
		// which never compares the container, so that push still lands.
		r.WriteString(` data-ignore-morph`)
	}
	if parent.declare && len(child.order) > 0 {
		var buf bytes.Buffer
		writeSignalsAttr(&buf, child.order, child.initial, parent.declareOnly, parent.declareSeen)
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
	ic, ok := v.(Initer)
	if !ok {
		return
	}
	err := ic.OnInit(child)
	child.initDone = true // ticks/subs are snapshotted from here on — see Tick/Listen
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
// push); it inits the child's nested CHILDREN — see inheritRequestScope.
func renderChildBind(key string, inst instance, base string, from *Ctx, rev *revertSet) (*Ctx, []byte) {
	c := newCtx()
	c.rev = rev
	c.isChild = true
	c.childKey = key
	c.unitV = inst
	c.base = base
	inheritRequestScope(c, from)
	return c, renderChildInner(c, inst.v)
}
