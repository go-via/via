package via

import (
	"bytes"
	"maps"
	"slices"
	"sync/atomic"

	"github.com/go-via/via/internal/hcore"
)

// renderRootBase renders inst into <div id="root">…</div> and returns the bind
// Ctx plus the bytes. It never hydrates from request echoes: every render that
// reaches here follows an action, so the answer must reflect mutated server
// state.
//
// declareSignals false is what a live push passes: re-declaring data-signals on
// every push would re-merge and clobber a signal the user is mid-edit (their
// half-typed message vanishing when someone else's arrives). Server-driven
// signal changes ride an explicit signal-patch instead. only restricts the
// declaration further, for a plain action's patch.
func renderRootBase(inst instance, declareSignals bool, base string, only map[string]any, seen map[string]bool, from *Ctx, rev *revertSet, badDecodeLogged *atomic.Bool) (*Ctx, []byte) {
	ctx := newRootCtx(declareSignals, base, only)
	ctx.declareSeen = seen
	ctx.unitV = inst
	ctx.rev = rev
	ctx.badDecodeLogged = badDecodeLogged
	inheritRequestScope(ctx, from)
	return ctx, renderRootWith(ctx, inst.v)
}

// inheritRequestScope carries a request-scoped render's wiring onto a later
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
	// The resolved handle, not just the manager: a session minted this request
	// has its cookie on w and nothing in req, so a re-resolve here would miss it
	// and mint a second id (and a second Set-Cookie).
	ctx.session = from.session
	// A child's mutation landed on the copy via.Child made at the previous
	// render. A root walk from here would call via.Child(parent.Field) again
	// and re-copy the parent's untouched field, throwing it away (validation
	// errors gone, submitted values back to empty) — so carry the instance down
	// for the walk to substitute at its own key. A root action mutates the very
	// instance the walk starts from, so it needs nothing.
	if from.isChild {
		ctx.actedKey, ctx.actedInst = from.childKey, from.unitV
	}
}

// newRootCtx builds the root bind Ctx for one render. A request-scoped
// transport takes it before rendering so runOnInit registers the root's
// Tick/Listen on the very Ctx the render (and the liveness verdict) reads.
func newRootCtx(declareSignals bool, base string, only map[string]any) *Ctx {
	ctx := newCtx()
	ctx.declare = declareSignals // children declare their own signals only on a declaring render
	ctx.declareOnly = only
	ctx.base = base
	return ctx
}

// renderRootWith renders v under an already-built root Ctx. Liveness is an
// output, not an input, so the nesting rules can only be checked once the
// whole walk is done — see checkLiveNesting.
func renderRootWith(ctx *Ctx, v viewer) []byte {
	declareSignals, only := ctx.declare, ctx.declareOnly
	prebindSignals(ctx, ctx.unitV)
	ctx.viewRan = true
	rr := hcore.NewRenderer(binderCtx{ctx})
	rr.Render(v.View())
	var b bytes.Buffer
	b.WriteString(`<div id="root"`)
	if declareSignals {
		order, initial := withWritten(ctx, only)
		writeSignalsAttr(ctx.logger(), &b, order, initial, only, ctx.declareSeen)
	}
	b.WriteString(`>`)
	b.Write(rr.Bytes())
	b.WriteString(`</div>`)
	out := b.Bytes()
	checkLiveNesting(ctx, false)
	return out
}

// withWritten adds the slots an action wrote that no View in the tree declared.
// A restricted declaration is filtered against what the render declared, so an
// island signal nobody renders has no entry for the filter to find and its Set
// would ship nothing. written is nil for a first paint, which declares all.
func withWritten(c *Ctx, written map[string]any) ([]string, map[string]any) {
	if len(written) == 0 {
		return c.order, c.initial
	}
	declared := c.slotSet()
	var extra []string
	for slot := range written {
		if !declared[slot] {
			extra = append(extra, slot)
		}
	}
	if extra == nil {
		return c.order, c.initial
	}
	slices.Sort(extra) // map order would make the attribute differ between identical renders
	initial := maps.Clone(c.initial)
	for _, slot := range extra {
		initial[slot] = written[slot]
	}
	return append(slices.Clone(c.order), extra...), initial
}

// checkLiveNesting enforces the two deferred-feature rules on the finished
// render tree: a live unit may not hold another live unit, and a live child's
// View may not call Child at all (a live root may — its plain children are
// re-inited on every push, see rootPush). It runs after the walk because
// liveness is only knowable then — loud and early, rather than serving a page
// that silently misroutes an action.
func checkLiveNesting(c *Ctx, underLive bool) {
	if c.live {
		if underLive {
			panic("via: via.Child: a live unit cannot sit inside another live unit — " +
				"embed it directly from a plain ancestor instead")
		}
		if c.isChild && len(c.children) > 0 {
			panic("via: via.Child: a live child's View must not call Child — keep a live child's View flat")
		}
	}
	for _, ch := range c.children {
		checkLiveNesting(ch, underLive || c.live)
	}
}

// checkLiveUnderLive is checkLiveNesting's first rule alone (a live unit
// cannot sit inside another live unit), for a re-render that has no parent
// Ctx to walk from — renderChildBind rebuilds just one unit's own subtree, so
// c here is that unit's synthetic root, not a node reached by a root-to-leaf
// walk. The second rule (a live child's View must not call Child at all) is
// deliberately not re-checked here: it is a discovery-render-only contract —
// hydration can legally reveal a Child call after the gate it depends on
// flips, and re-enforcing flatness on every push would refuse that legal
// shape instead of just the one this guards, a live unit gaining a live
// descendant with no ancestor left to register it.
func checkLiveUnderLive(c *Ctx, underLive bool) {
	if c.live && underLive {
		panic(liveNestingViolation{msg: "via: via.Child: a live unit cannot sit inside another live unit — " +
			"embed it directly from a plain ancestor instead"})
	}
	for _, ch := range c.children {
		checkLiveUnderLive(ch, underLive || c.live)
	}
}

// liveNestingViolation is checkLiveUnderLive's panic value. Unlike a render
// panic from user code, this rule is a fixed property of the tree shape: the
// same push will hit it again on every following tick. childPush recovers it
// by type (see connect.go) to abort the stream once instead of retrying it
// forever the way runPushItem's generic per-item recover would.
type liveNestingViolation struct{ msg string }
