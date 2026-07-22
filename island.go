package via

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// anyLiveIsland reports whether a completed render discovered an embedded
// child that is a live island (implements OnConnect) — so the page
// knows to bootstrap the SSE stream. Read off the real render's Ctx, never a
// zero probe (whose injected deps would be nil), keeping detection panic-safe.
func anyLiveIsland(ctx *Ctx) bool {
	for _, isl := range ctx.islands {
		if _, ok := isl.islandV.(Live); ok {
			return true
		}
	}
	return false
}

// renderIslandPatch wraps island idx's re-rendered child in its container for an
// element-patch — Datastar morphs it onto #via-i{idx}, leaving siblings alone.
func renderIslandPatch(idx int, v viewer) []byte {
	var b bytes.Buffer
	b.WriteString(`<div id="via-i` + strconv.Itoa(idx) + `">`)
	b.Write(renderIsland(idx, v))
	b.WriteString(`</div>`)
	return b.Bytes()
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
// via.When, not an empty child.
//
// It panics if the child has no View() method, and if a live child is embedded
// inside another island — islands are discovered flat, so a nested live island
// would render once and never stream. Both are wrote-it-wrong errors, loud at
// the first render, never a silent blank or dead region.
func Embed[C any](child C) h.H {
	v, isView := any(&child).(viewer)
	if !isView {
		panic("via: via.Embed(child) requires child to have a View() method")
	}
	return hcore.Dyn(func(r *hcore.Renderer) { embedViewer(r, v) })
}

// embedViewer is the positional-island wiring behind Embed: it renders v into
// its own container <div id="via-i{idx}">, binds
// its signals/actions into an island-scoped Ctx (idx prefixes its slots, so
// siblings never collide), and appends it to the parent's islands slice so a
// push or action patches exactly this one. A non-Ctx binder is a bare render
// with no parent to attach to, so it writes nothing.
func embedViewer(r *hcore.Renderer, v viewer) {
	parent := ctxOf(r.Binder())
	if parent == nil {
		return
	}
	_, live := v.(Live)
	// Islands are discovered flat (the connect walk never descends into an
	// island's own embeds), so a live child nested inside another island would
	// get no stream wiring — no ticks, no pushes, orphaned signals. Refuse it
	// loudly instead of rendering a dead region.
	if live && parent.isIsland {
		panic("via: nested live islands are unsupported — embed the live child in the page's View, not inside another island")
	}
	idx := len(parent.islands)
	child := newCtx(parent.inSignals)
	child.isIsland = true
	child.islandIdx = idx
	child.islandV = v
	// A live island's View reads server State[T], which is gated on the
	// live-island flag — set it so the child renders inside its own island.
	child.island = live
	parent.islands = append(parent.islands, child)

	// Render first so the child's signal slots (order/initial) are populated,
	// then declare them on the container — on a declaring render (first paint)
	// only. A live push omits the declaration (renderIslandPatch), so a morph
	// never re-merges a signal the user is editing.
	child.rendered = renderIslandInner(child, v)
	r.WriteString(`<div id="via-i` + strconv.Itoa(idx) + `"`)
	if parent.declare && len(child.order) > 0 {
		var buf bytes.Buffer
		writeSignalsAttr(&buf, child.order, child.initial)
		r.WriteString(buf.String())
	}
	r.WriteString(`>`)
	r.WriteString(string(child.rendered))
	r.WriteString(`</div>`)
}

// renderIslandInner renders the island's View with child as the binder, so the
// child's actions/signals bind into its own tables. Returns the inner HTML
// (without the container div), already escaped.
func renderIslandInner(child *Ctx, v viewer) []byte {
	rr := hcore.NewRenderer(binderCtx{child})
	rr.Render(v.View())
	return rr.Bytes()
}

// bindIsland renders island idx's child with hydration `in` into a fresh
// island-scoped Ctx and returns it — used by a live action to fill the island's
// positional action table before running one. The rendered bytes are discarded;
// only the bound actions (and any dirty signals) matter.
func bindIsland(idx int, v viewer, in map[string]json.RawMessage) *Ctx {
	c := newCtx(in)
	c.isIsland = true
	c.islandIdx = idx
	c.islandV = v
	_, c.island = v.(Live)
	hcore.NewRenderer(binderCtx{c}).Render(v.View())
	return c
}

// renderIsland re-renders island idx's child (no hydration, so it reflects
// post-action state) for an element-patch response.
func renderIsland(idx int, v viewer) []byte {
	c := newCtx(nil)
	c.isIsland = true
	c.islandIdx = idx
	c.islandV = v
	_, c.island = v.(Live) // a live island's State[T] reads need the live flag
	return renderIslandInner(c, v)
}
