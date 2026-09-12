package via

import (
	"bytes"
	"strconv"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// renderPass allocates flat, page-wide island indices and shape-digest
// placeholder tokens during one root-level render — shared by pointer across
// a whole tree of Embeds so a live descendant's container id (and dispatch
// address) can never collide with an unrelated one elsewhere on the page. It
// is created once per renderRootBase call and never forked: an island's own
// standalone re-render (renderIslandBind) never calls Embed itself (see
// Embed's godoc on nested composition), so it never needs one of its own.
type renderPass struct {
	n    int
	dTok int // next shape-digest placeholder token, unique within this pass
}

func (p *renderPass) next() int {
	idx := p.n
	p.n++
	return idx
}

// nextDigestToken mints a placeholder unique within this render pass — every
// unit's action(s) share their own token, so a later per-unit substitution
// pass can't confuse one unit's shape digest for another's.
func (p *renderPass) nextDigestToken() string {
	tok := p.dTok
	p.dTok++
	return "\x00vD" + strconv.Itoa(tok) + "\x00"
}

// Embed renders a child composition — a plain struct field of the parent,
// seeded at the parent's literal — into its own positional container within the
// parent's View. The child may be plain (just structure + actions) or live (it
// implements OnConnect) — when live it becomes an independent region patched
// over the parent's one SSE stream; when not, its actions re-render it in place.
// Liveness is an interface assertion on C, never a separate type:
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
// via.When, not an empty child. Plain (non-live) composition nests to any
// depth — a layout may embed a composition that itself embeds another.
//
// A live island, however, may only be embedded directly by a page whose root
// is NOT itself live (the root, or a plain wrapper reached only through
// further plain Embeds, may hold it) — a live island can never be embedded
// inside another live composition, nor may a live island's own View call
// Embed at all. Nested live composition (a dynamic set of live children
// addressed by identity) is a deferred feature; violating either rule
// panics at render, loud and early, rather than silently misrouting an action.
//
// An Embed's position among the page's Embed calls is its identity — the
// container id, the signal prefix, and the dispatch address are all derived
// from render order. That position must be the same on every render for the
// life of a connection: a When wrapped around an Embed must depend only on
// data fixed by OnInit or the field literal, never on time, a client signal,
// or shared state that changes while the page is open.
//
// It panics if the child has no View() method — a wrote-it-wrong error, loud
// at the first render, never a silent blank or dead region.
func Embed[C any](child C) h.H {
	v, isView := any(&child).(viewer)
	if !isView {
		panic("via: via.Embed(child) requires child to have a View() method")
	}
	return hcore.Dyn(func(r *hcore.Renderer) { embedViewer(r, v) })
}

// embedViewer is the positional-island wiring behind Embed: it renders v into
// its own container <div id="via-i{idx}">, binds its signals/actions into an
// island-scoped Ctx (idx prefixes its slots, so siblings never collide), and
// appends it to the parent's islands slice so a push or action patches
// exactly this one. A non-Ctx binder is a bare render with no parent to
// attach to, so it writes nothing.
func embedViewer(r *hcore.Renderer, v viewer) {
	parent := ctxOf(r.Binder())
	if parent == nil {
		return
	}

	// An embedded live unit's own View calling Embed is exactly the nested
	// composition A1 cut from v0.8: its own independent re-render (via
	// renderIslandBind) never re-walks a parent, so it has nothing to
	// replicate stable addressing from — the deleted childSlots/renderPass
	// forking existed only to paper over that. Refuse it outright instead.
	if parent.isIsland && parent.island {
		panic("via: via.Embed: a live island's own View must not call Embed — " +
			"nested live composition is deferred; keep a live island's View flat")
	}
	// A live child anywhere under a live unit (the immediate parent, or any
	// ancestor reached only through plain Embeds) has the same problem in
	// reverse: the live ANCESTOR's own re-render would re-seed the live
	// child from its field literal every time, with no reuse mechanism left
	// to preserve its state. One live unit per page: the root, or a live
	// island embedded directly (or via plain wrappers) from a plain root.
	_, live := v.(Live)
	if live && (parent.island || parent.underLive) {
		panic("via: via.Embed: a live island cannot be embedded inside another live composition — " +
			"nested live composition is deferred; embed it directly from a plain root instead")
	}

	ordinal := len(parent.islands) // this parent's k-th Embed call this render

	// pass.next() must advance exactly once per Embed call, or a later
	// sibling's page-wide index collides with (or skips) this one's.
	idx := ordinal
	if parent.pass != nil {
		idx = parent.pass.next()
	}

	child := newCtx(parent.inSignals)
	child.isIsland = true
	child.islandIdx = idx
	child.islandV = v
	child.base = parent.base // the mount prefix, so the island's own action URLs carry it too
	// A live island's View reads server State[T], which is gated on the
	// live-island flag — set it so the child renders inside its own island.
	child.island = live
	child.underLive = parent.island || parent.underLive
	child.pass = parent.pass
	parent.islands = append(parent.islands, child)

	// Render first so the child's signal slots (order/initial) are populated,
	// then declare them on the container — on a declaring render (first paint)
	// only. A live push omits the declaration (renderIslandBind), so a morph
	// never re-merges a signal the user is editing.
	child.rendered = renderIslandInner(child, v)
	r.WriteString(`<div id="via-i` + strconv.Itoa(idx) + `"`)
	if live {
		// Datastar only skips a morph when BOTH the existing element and the
		// incoming fragment carry the attribute — so every root-walk render
		// marks the container, and a plain root's own patch leaves it alone.
		// The island's own push targets #via-i{idx} in inner mode instead,
		// which never compares the container, so the push still lands.
		r.WriteString(` data-ignore-morph`)
	}
	if parent.declare && len(child.order) > 0 {
		var buf bytes.Buffer
		writeSignalsAttr(&buf, child.order, child.initial, parent.declareOnly)
		r.WriteString(buf.String())
	}
	r.WriteString(`>`)
	r.WriteString(string(child.rendered))
	r.WriteString(`</div>`)
}

// renderIslandInner renders the island's View with child as the binder, so the
// child's actions/signals bind into its own tables, then substitutes child's
// own shape-digest placeholder (if it wrote any action) into the finished
// bytes — child.shapeDigest is only knowable once its own render is done.
// Returns the inner HTML (without the container div), already escaped.
func renderIslandInner(child *Ctx, v viewer) []byte {
	rr := hcore.NewRenderer(binderCtx{child})
	rr.Render(v.View())
	out := rr.Bytes()
	if child.digestPH != "" {
		out = bytes.ReplaceAll(out, []byte(child.digestPH), []byte(child.shapeDigest()))
	}
	return out
}

// renderIslandBind re-renders island idx's child (no hydration, so it
// reflects post-action state) and returns its bind Ctx alongside the inner
// HTML. A stateless action's response needs the Ctx too: only this render's
// order/initial values (via writeSignalsAttr) let the response ship a
// Signal.Set the action wrote. A live island's own View can never itself
// call Embed (see Embed's godoc), so this Ctx needs no render pass or live
// connection of its own — there is nothing nested left to number or reuse.
func renderIslandBind(idx int, v viewer, base string) (*Ctx, []byte) {
	c := newCtx(nil)
	c.isIsland = true
	c.islandIdx = idx
	c.islandV = v
	c.base = base
	_, c.island = v.(Live) // a live island's State[T] reads need the live flag
	return c, renderIslandInner(c, v)
}
