package via

import (
	"bytes"
	"fmt"
	"strconv"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// renderPass allocates flat, page-wide island indices during one discovery
// render — shared by pointer across a whole tree of Embeds so a live
// descendant's container id (and dispatch address) can never collide with an
// unrelated one elsewhere on the page, regardless of nesting depth.
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

// renderIslandPatch re-renders island idx's child for an element-patch — Datastar
// morphs it onto #via-i{idx}, leaving siblings alone. base is the mount prefix,
// so the island's own action URLs carry it too. conn is the live connection
// driving this render (nil for a stateless action's own re-render), so a
// deeper live descendant embedded in this island can be found and reused
// rather than reseeded from a fresh copy. Returns the render's bind Ctx
// alongside the framed bytes: a live push keeps it as the island's current unit
// so the next action's actions/hydrators table is the one this render bound.
func renderIslandPatch(idx int, v viewer, base string, conn *liveConn) (*Ctx, []byte) {
	c, inner := renderIslandBind(idx, v, base, conn)
	var b bytes.Buffer
	b.WriteString(`<div id="via-i` + strconv.Itoa(idx) + `">`)
	b.Write(inner)
	b.WriteString(`</div>`)
	return c, b.Bytes()
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
// A live composition may itself embed live children, at any depth: each
// streams and patches independently over the page's one connection.
//
// Generic layouts fall out for free: type Shell[C any] struct{ Body C } with
// via.Embed(s.Body) composes one layout with any page. An optional region is
// via.When, not an empty child.
//
// A live parent's Embed identity is tracked by (parent, ordinal) plus the
// child's own concrete type, so a via.When condition flipping around an
// Embed is safe: the sibling that shifts onto the freed ordinal is never
// mistaken for the vanished one. It CANNOT tell apart two DIFFERENT fields of
// the SAME concrete type swapping ordinals this way (e.g. via.When(cond,
// embedX) followed by via.Embed(otherX) of the same type X) — the reused
// instance in that case may be either one. Guard that case behind two
// distinct types, or keep the conditional child's ordinal fixed (e.g. always
// call Embed and let the child itself render nothing when hidden).
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
	ordinal := len(parent.islands) // this parent's k-th Embed call this render

	// pass.next() must advance exactly once per Embed call — on both the
	// reuse and the fresh-seed path below — or a later sibling's page-wide
	// index collides with (or skips) this one's.
	idx := ordinal
	if parent.pass != nil {
		idx = parent.pass.next()
	}

	// A live parent's own re-render calls Embed again, seeding a fresh
	// by-value copy from its field — reusing the already-connected child's
	// own instance (and its islandIdx) instead is what keeps the child's
	// state, and its container id, stable across the parent's re-renders.
	// The reused instance is only still valid if its own islandIdx still
	// matches this render's page-wide numbering AND it is still the same
	// concrete type — a via.When-guarded sibling appearing/disappearing
	// shifts every later Embed's ordinal, so islandIdx alone can land two
	// DIFFERENT children on the same slot (see sameConcreteType). Either
	// mismatch means the stale entry belongs to whatever now sits at idx,
	// not to this Embed call, so fall through and re-seed instead.
	if parent.conn != nil {
		if existing, ok := parent.conn.childAt(unitAddr(parent), ordinal); ok && existing.islandIdx == idx && sameConcreteType(existing.islandV, v) {
			renderConnectedChild(r, parent, existing, ordinal)
			return
		}
	}

	_, live := v.(Live)
	child := newCtx(parent.inSignals)
	child.isIsland = true
	child.islandIdx = idx
	child.islandV = v
	child.base = parent.base // the mount prefix, so the island's own action URLs carry it too
	// A live island's View reads server State[T], which is gated on the
	// live-island flag — set it so the child renders inside its own island.
	child.island = live
	child.pass = parent.pass
	child.conn = parent.conn
	child.parentUnit = unitAddr(parent)
	child.ordinal = ordinal
	parent.islands = append(parent.islands, child)

	// Render first so the child's signal slots (order/initial) are populated,
	// then declare them on the container — on a declaring render (first paint)
	// only. A live push omits the declaration (renderIslandPatch), so a morph
	// never re-merges a signal the user is editing.
	child.rendered = renderIslandInner(child, v)
	r.WriteString(`<div id="via-i` + strconv.Itoa(idx) + `"`)
	if parent.declare && len(child.order) > 0 {
		var buf bytes.Buffer
		writeSignalsAttr(&buf, child.order, child.initial, parent.declareOnly)
		r.WriteString(buf.String())
	}
	r.WriteString(`>`)
	r.WriteString(string(child.rendered))
	r.WriteString(`</div>`)
}

// sameConcreteType reports whether a and b share their dynamic type — the
// other half of the childSlots reuse check, alongside islandIdx. %T (not
// reflect.TypeOf — via wiring stays reflection-free) is enough: we only need
// equality, never the type itself. It cannot tell apart two children of the
// SAME type that swap ordinals (e.g. two via.When branches embedding the
// same composition); see Embed's godoc for that residual constraint.
func sameConcreteType(a, b viewer) bool {
	return fmt.Sprintf("%T", a) == fmt.Sprintf("%T", b)
}

// renderConnectedChild re-renders an already-connected live descendant's OWN
// instance (existing.islandV, the pointer every one of its own actions/ticks
// has been mutating) in place of the fresh by-value copy Embed just made —
// the fix for a live parent's re-render otherwise clobbering the child's
// state. It reuses existing's islandIdx (so the container id and dispatch
// address are unchanged) and re-registers the fresh bind so the next action
// against the child runs against THIS render's actions/hydrators table.
func renderConnectedChild(r *hcore.Renderer, parent, existing *Ctx, ordinal int) {
	c, inner := renderIslandBind(existing.islandIdx, existing.islandV, parent.base, parent.conn)
	c.push = existing.push // the push closure is fixed for the unit's whole life; carry it over
	c.parentUnit, c.ordinal = unitAddr(parent), ordinal
	parent.conn.replace(c)
	parent.islands = append(parent.islands, c)
	r.WriteString(`<div id="via-i` + strconv.Itoa(existing.islandIdx) + `">`)
	r.WriteString(string(inner))
	r.WriteString(`</div>`)
}

// renderIslandInner renders the island's View with child as the binder, so the
// child's actions/signals bind into its own tables, then substitutes child's
// own shape-digest placeholder (if it wrote any action) into the finished
// bytes — child.shapeDigest is only knowable once its whole render (including
// any further-nested Embeds) is done. Returns the inner HTML (without the
// container div), already escaped.
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
// HTML. conn is threaded down so a live descendant embedded inside this
// island is itself found and reused rather than reseeded. A stateless
// action's response needs the Ctx too: only this render's order/initial
// values (via writeSignalsAttr) let the response ship a Signal.Set the
// action wrote.
func renderIslandBind(idx int, v viewer, base string, conn *liveConn) (*Ctx, []byte) {
	c := newCtx(nil)
	c.isIsland = true
	c.islandIdx = idx
	c.islandV = v
	c.base = base
	// A standalone re-render of just this island (a push or a stateless
	// action's patch) still must hand out the SAME page-wide indices its
	// nested Embeds got at first paint — the shared pass consumed idx for
	// this island right before descending into its own View, so its
	// descendants' numbering picked up at idx+1. A fresh allocator starting
	// at 0 would renumber them (colliding with whatever else sits at low
	// indices elsewhere on the page) purely because this island happened to
	// be re-rendered on its own.
	c.pass = &renderPass{n: idx + 1}
	c.conn = conn
	_, c.island = v.(Live) // a live island's State[T] reads need the live flag
	return c, renderIslandInner(c, v)
}
