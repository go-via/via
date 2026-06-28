package via

import (
	"bytes"
	"encoding/json"
	"strconv"

	"github.com/go-via/via/v2/h"
)

// anyLiveIsland reports whether a completed render discovered an embedded
// Island[C] child that is a live island (implements OnConnect) — so the page
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

// Island is a value-field handle that embeds a child composition C as an
// independent live region (an "island") within a parent's View. Like Signal[T]
// and State[T] it is declared as a struct field and bound by the runtime, never
// reflected over:
//
//	type Page struct{ Chat via.Island[ChatRoom]; Ticker via.Island[Clock] }
//	func (p *Page) View() h.H { return h.Div(p.Chat.Embed(), p.Ticker.Embed()) }
//
// Because Island is a value field, via's per-connection copy of the parent
// copies the child with it, so each connection (and, for a stateless page, each
// request) gets an isolated child instance and its own State — no '&', no shared
// pointer. The child's View/OnConnect are found by interface assertion, not
// reflection.
type Island[C any] struct{ child C }

// NewIsland seeds an embedded island's child by value — use it to inject the
// child's dependencies (a shared *Room, a store) at registration, e.g.
// via.Register(Page{Chat: via.NewIsland(ChatRoom{room: room})}). The seed is
// taken by value (no '&'); via's per-connection copy of the parent copies the
// child with it, so value-typed state stays isolated while pointer deps (the
// shared room) are intentionally shared. A zero Island[C] is still valid — use
// NewIsland only when the child needs seeding.
func NewIsland[C any](child C) Island[C] { return Island[C]{child: child} }

// Embed renders the island's child into its own positional container and binds
// the child's actions to an island-scoped path (/_via/a/{island}/{n}), so
// sibling islands stay independent and a push or action patches exactly one of
// them. Pointer receiver, so p.Chat.Embed() auto-addresses the field — no '&'.
func (i *Island[C]) Embed() h.H {
	return h.Dyn(func(r *h.Renderer) {
		parent, ok := r.Binder().(*Ctx)
		if !ok {
			return
		}
		v, isView := any(&i.child).(viewer)
		if !isView {
			panic("via: Island[C] requires C to have a View() method")
		}
		idx := len(parent.islands)
		child := newCtx(parent.inSignals)
		child.isIsland = true
		child.islandIdx = idx
		child.islandV = v
		// A live island's View reads server State[T], which is gated on the
		// live-island flag — set it so the child renders inside its own island.
		_, child.island = v.(Live)
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
	})
}

// renderIslandInner renders the island's View with child as the binder, so the
// child's actions/signals bind into its own tables. Returns the inner HTML
// (without the container div), already escaped.
func renderIslandInner(child *Ctx, v viewer) []byte {
	rr := h.NewRenderer(child)
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
	h.NewRenderer(c).Render(v.View())
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
