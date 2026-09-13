package via

import (
	"bytes"
	"unsafe"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// Embed renders a child composition — a plain struct field of the parent,
// seeded at the parent's literal — into its own positional container within the
// parent's View. The child gets its own OnInit (run before its View, on every
// request-scoped render) and may be plain (just structure + actions) or live —
// when live it becomes an independent region patched over the parent's one SSE
// stream; when not, its actions re-render it in place. Liveness is what the
// child DOES, never a separate type: it registered a Tick/Listen in OnInit, or
// its View rendered a State/List. One live embed anywhere in the tree is what
// makes the page stream; a page with none is served plain.
//
//	type Page struct{ Chat ChatRoom; Ticker Clock }
//	func (p *Page) View() h.H { return h.Div(via.Embed(p.Chat), via.Embed(p.Ticker)) }
//	via.Register(Page{Chat: ChatRoom{room: room}})
//
// The child is taken by value (no '&'): the field literal seeds it, and after
// the first paint the connection's own registered instance carries its State —
// value state stays isolated per connection while pointer deps (a shared room)
// are intentionally shared. Embed's argument must be a field selector (p.Chat),
// never a composite literal — a literal would re-seed on every render.
//
// Generic layouts fall out for free: type Shell[C any] struct{ Body C } with
// via.Embed(s.Body) composes one layout with any page. An optional region is
// via.When, not an empty child. Plain composition nests to any depth — a layout
// may embed a composition that itself embeds another.
//
// The whole liveness rule is one line:
//
//	A page streams iff it contains a live unit; a live embed may sit under any
//	plain ancestor; a live unit may not contain another live unit.
//
// So nesting is open, not restricted: embed live children as deep as you like,
// as long as every ancestor on the path is plain. Only the last clause is a
// limit — a live embed's own View may not call Embed, and a live unit may not be
// embedded beneath another live one (a dynamic set of live children addressed by
// identity is a deferred feature). Violating it panics at render, loud and early,
// rather than silently misrouting an action.
//
// An Embed's embed KEY is its position among its own parent's Embed calls,
// composed with the parent's key: the root's children are "0", "1", …, and a
// child of "0" is "0-0". The container id (via-i0-0), the signal prefix
// (i0-0_) and the dispatch address (/_via/a/0-0/…) all read that one key, so
// re-rendering any subtree on its own numbers its descendants identically to
// the full-page walk. A child's key must be the same on every render for the
// life of a connection: a When wrapped around an Embed shifts its later
// SIBLINGS' ordinals, so such a When must depend only on data fixed by OnInit
// or the field literal, never on time, a client signal, or shared state that
// changes while the page is open.
//
// It panics if the child has no View() method — a wrote-it-wrong error, loud
// at the first render, never a silent blank or dead region.
func Embed[C any](child C) h.H {
	v, isView := any(&child).(viewer)
	if !isView {
		panic("via: via.Embed(child) requires child to have a View() method")
	}
	// &child, not the parent's field: the copy is what the child's View binds
	// against for the life of this render (and, for a live embed, for the life
	// of the connection), so its address is the base its signals offset from.
	inst := instance{v: v, base: unsafe.Pointer(&child), size: unsafe.Sizeof(child)}
	return hcore.Dyn(func(r *hcore.Renderer) { embedViewer(r, inst) })
}

// embedViewer is the embed wiring behind Embed: it renders v into its own
// container <div id="via-i{key}">, binds its signals/actions into an
// embed-scoped Ctx (the key prefixes its slots, so siblings never collide),
// and appends it to the parent's embeds slice so a push or action patches
// exactly this one. A non-Ctx binder is a bare render with no parent to
// attach to, so it writes nothing.
func embedViewer(r *hcore.Renderer, inst instance) {
	parent := ctxOf(r.Binder())
	if parent == nil {
		return
	}

	key := parent.childKey(len(parent.embeds))

	child := newCtx(parent.inSignals)
	child.isEmbed = true
	child.embedKey = key
	child.embedV = inst
	child.base = parent.base // the mount prefix, so the embed's own action URLs carry it too
	child.declareSeen = parent.declareSeen
	child.req = parent.req
	child.sessions = parent.sessions
	child.sessW = parent.sessW
	parent.embeds = append(parent.embeds, child)

	// Only a request-scoped render inits: a live push re-renders the whole
	// tree on every tick, and re-running a child's OnInit there would reload
	// its data — and re-register its Tick/Listen — once per beat.
	if parent.doInit {
		child.doInit = true
		initChild(child, inst.v)
	}

	// Render first so the child's signal slots (order/initial) are populated,
	// then declare them on the container — on a declaring render (first paint)
	// only. A live push omits the declaration (renderEmbedBind), so a morph
	// never re-merges a signal the user is editing. The render is also what
	// settles child.live, so the container attribute below can only be decided
	// after it.
	child.rendered = renderEmbedInner(child, inst.v)
	r.WriteString(`<div id="via-i` + key + `"`)
	if child.live {
		// Datastar only skips a morph when BOTH the existing element and the
		// incoming fragment carry the attribute — so every root-walk render
		// marks the container, and a plain root's own patch leaves it alone.
		// The embed's own push targets #via-i{key} in inner mode instead,
		// which never compares the container, so the push still lands.
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

// childInit is the panic sentinel initChild throws when an embedded child's
// OnInit fails: the render is already deep inside the parent's View, with no
// return path left, so the transport's own recover turns it back into the
// answer OnInit asked for (see recoverToHTTP).
type childInit struct {
	err      error
	redirect string
}

// initChild runs an embedded child's OnInit before its View. A paramMiss panic
// propagates untouched — the transport already answers it as a 404.
func initChild(child *Ctx, v any) {
	ic, ok := v.(Initer)
	if !ok {
		return
	}
	err := ic.OnInit(child)
	child.initDone = true // ticks/subs are snapshotted from here on — see Tick/Listen
	if err != nil {
		panic(childInit{err: err})
	}
	if child.redirect != "" {
		panic(childInit{redirect: child.redirect})
	}
}

// renderEmbedInner renders the embed's View with child as the binder, so the
// child's actions/signals bind into its own tables. Returns the inner HTML (without the container div), already escaped.
func renderEmbedInner(child *Ctx, v viewer) []byte {
	rr := hcore.NewRenderer(binderCtx{child})
	rr.Render(v.View())
	return rr.Bytes()
}

// renderEmbedBind re-renders the embed at key (no hydration, so it reflects
// post-action state) and returns its bind Ctx alongside the inner HTML. A
// plain action's response needs the Ctx too: only this render's
// order/initial values (via writeSignalsAttr) let the response ship a
// Signal.Set the action wrote. Seeding the Ctx with the embed's own key is
// what keeps a PLAIN embed's re-render — whose View may well Embed further
// children — numbering those children exactly as the full-page walk did,
// instead of restarting at the root's own key and aliasing onto it.
func renderEmbedBind(key string, inst instance, base string) (*Ctx, []byte) {
	c := newCtx(nil)
	c.isEmbed = true
	c.embedKey = key
	c.embedV = inst
	c.base = base
	return c, renderEmbedInner(c, inst.v)
}
