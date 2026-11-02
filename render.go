package via

import (
	"bytes"
	"log/slog"
	"sync/atomic"

	"github.com/go-via/via/internal/render"
)

// rerender is what a render after an action or on a push carries over from
// the request or connection it belongs to. from is the request-scoped Ctx the
// render belongs to; it inits nested children — see inheritRequestScope.
type rerender struct {
	base            string
	from            *Ctx
	rev             *revertSet
	badDecodeLogged *atomic.Bool
}

func (r rerender) wire(ctx *Ctx) {
	ctx.rev = r.rev
	ctx.badDecodeLogged = r.badDecodeLogged
	inheritRequestScope(ctx, r.from)
}

// renderRootBase renders inst into <div id="root">…</div> and returns the bind
// Ctx plus the bytes. It never hydrates from request echoes: every render that
// reaches here follows an action, so the answer must reflect mutated server
// state.
//
// declareSignals false is what an action answer and a live push pass:
// re-declaring data-signals would re-merge and clobber a signal the user is
// mid-edit (their half-typed message vanishing when someone else's arrives),
// and a morph skips a data-signals text the element already carries, so a Set
// back to that value would never land. Server-driven signal changes ride an
// explicit signal-patch instead.
func renderRootBase(inst instance, declareSignals bool, r rerender) (*Ctx, []byte) {
	ctx := newRootCtx(declareSignals, r.base)
	ctx.unitV = inst
	r.wire(ctx)
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
	// A re-render of a live unit is live whether or not this View re-derives
	// it: a Tick or Listen that made it live is not registered again here.
	ctx.live = from.live
	ctx.reqScope = from.reqScope
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
func newRootCtx(declareSignals bool, base string) *Ctx {
	ctx := newCtx()
	ctx.declare = declareSignals // children declare their own signals only on a declaring render
	ctx.base = base
	return ctx
}

// renderRootWith renders v under an already-built root Ctx. Liveness is an
// output, not an input, so the nesting rules can only be checked once the
// whole walk is done — see checkLiveNesting.
func renderRootWith(ctx *Ctx, v viewer) []byte {
	prebindSignals(ctx, ctx.unitV)
	ctx.viewRan = true
	rr := render.NewRenderer(binderCtx{ctx})
	rr.Render(v.View())
	var b bytes.Buffer
	b.WriteString(`<div id="root"`)
	if ctx.declare {
		writeSignalsAttr(ctx.logger(), &b, ctx.initial)
	}
	b.WriteString(`>`)
	b.Write(rr.Bytes())
	b.WriteString(`</div>`)
	out := b.Bytes()
	checkLiveNesting(ctx, false)
	return out
}

const liveUnderLiveMsg = "via: via.Child: a live unit cannot sit inside another live unit — " +
	"embed it directly from a plain ancestor instead"

// checkLiveNesting enforces the two deferred-feature rules on the finished
// render tree: a live unit may not hold another live unit, and a live child's
// View may not call Child at all (a live root may — its plain children are
// re-inited on every push, see unitPusher.from). It runs after the walk because
// liveness is only knowable then — loud and early, rather than serving a page
// that silently misroutes an action.
func checkLiveNesting(c *Ctx, underLive bool) {
	if c.live {
		if underLive {
			panic(liveUnderLiveMsg)
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
		panic(liveNestingViolation{msg: liveUnderLiveMsg})
	}
	for _, ch := range c.children {
		checkLiveUnderLive(ch, underLive || c.live)
	}
}

// bootPolicy marks a boot render and discards what it would log. Children
// inherit their parent's policy, so the whole tree sees it.
var bootPolicy = &routerPolicy{log: slog.New(slog.DiscardHandler)}

// bootRender renders inst, a copy of the mounted literal, the way a live push
// does: no OnInit anywhere in the tree, and a Ctx of its own, so it has no
// side effects. It returns the message of a Miswired panic and swallows every
// other: a View that dereferences data OnInit would have loaded is not a bug.
func bootRender(inst instance) (miswired string) {
	defer recoverMiswired(&miswired)
	ctx := newRootCtx(false, "")
	ctx.unitV = inst
	ctx.policy = bootPolicy
	renderRootWith(ctx, inst.v)
	return ""
}

func recoverMiswired(msg *string) {
	if rec := recover(); rec != nil {
		if m, ok := rec.(render.Miswired); ok {
			*msg = string(m)
		}
	}
}

// liveNestingViolation is checkLiveUnderLive's panic value. Unlike a render
// panic from user code, this rule is a fixed property of the tree shape: the
// same push will hit it again on every following tick. unitPusher.recoverFatal
// recovers it by type to abort the stream once instead of retrying it
// forever the way runPushItem's generic per-item recover would.
type liveNestingViolation struct{ msg string }
