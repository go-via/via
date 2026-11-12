package via

import (
	"bytes"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"unsafe"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/actionid"
	"github.com/go-via/via/internal/render"
)

// Child renders a child composition — a struct field of the parent, passed by
// address — into its own container. The child gets its own OnInit (once per
// request, or once per connection on a live page, before its first View) and
// may be plain or live; liveness is what the child does, never a separate
// type. One live child anywhere makes the page stream.
//
//	type Page struct{ Chat ChatRoom; Ticker Clock }
//	func (p *Page) View() h.H { return h.Div(via.Child(&p.Chat), via.Child(&p.Ticker)) }
//
// The child's state lives in the parent's field: an action on the child, its
// Ticks and its OnInit all mutate that field, and the parent sees the result.
// A child's key is its field path in the root ("Chat", "Stats-A", "Rows-1" for
// an array element), so the container id, signal prefix and action URLs stay
// put whatever a via.When around a sibling does. An optional region is
// via.When, not an empty child; a generic layout (type Shell[C any] struct{
// Body C }) composes for free.
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
// It panics if *C has no View() h.H method, if child is not a field (or array
// element) of the composition whose View renders it — a local variable, a
// package var or a slice element has no key — and if one View renders the
// same field twice. A row of a slice is markup with on.Bind, not a Child.
func Child[C any](child *C) h.H {
	typ := reflect.TypeFor[C]()
	v, isView := any(child).(viewer)
	if !isView {
		panic(render.Miswired("via: via.Child(&x) requires *" + typ.String() + " to have a View() h.H method"))
	}
	checkViewReceiver(typ)
	inst := instance{v: v, base: unsafe.Pointer(child), size: unsafe.Sizeof(*child), typ: typ, sig: signalsOf(typ)}
	return render.Dyn(&childNode{inst: inst})
}

type childNode struct{ inst instance }

func (n *childNode) RenderTo(r *render.Renderer) { childViewer(r, n.inst) }

type childPathKey struct {
	parent, child reflect.Type
	off           uintptr
}

var childPaths sync.Map // childPathKey -> string

// childLocalKey is the field path of inst inside parent's composition: "Chat",
// "Stats-A", "Rows-1". It panics when inst is not a field of parent, since
// only a field shares the parent's lifetime and has a name to address it by.
func childLocalKey(parent *Ctx, inst instance) string {
	// An address outside the parent wraps off past its size, where no field is.
	k := childPathKey{parent: parent.unitV.typ, child: inst.typ, off: uintptr(inst.base) - uintptr(parent.unitV.base)}
	if v, ok := childPaths.Load(k); ok {
		return v.(string)
	}
	path, ok := "", false
	if k.parent != nil {
		path, ok = actionid.ChildPath(k.parent, k.off, k.child)
	}
	if !ok {
		panic(render.Miswired("via: via.Child(&x) needs x to be a field of " + unitTypeName(k.parent) +
			", the composition whose View renders it; the " + inst.typ.String() + " passed is not one of its " +
			"fields. Hold the child as a struct field (or an element of an array field) and pass its address."))
	}
	checkChildPrefix(k.parent, childSlotName(path))
	childPaths.Store(k, path)
	return path
}

// childSlotName is the signal-slot segment for a child at path. '-' is no JS
// identifier character, and Ref() hands slot names straight to Datastar
// expressions.
func childSlotName(path string) string { return lowerFirst(strings.ReplaceAll(path, "-", "_")) }

func unitTypeName(t reflect.Type) string {
	if t == nil {
		return "the parent"
	}
	return t.String()
}

// childViewer renders the child into <div id="via-i{key}">, binds its
// signals/actions into a child-scoped Ctx, and appends it to the parent's
// children so a push or action patches exactly this one. A non-Ctx binder is a
// bare render with no parent to attach to, so it writes nothing.
func childViewer(r *render.Renderer, inst instance) {
	parent := ctxOf(r.Binder())
	if parent == nil {
		return
	}
	checkChildHooks(parent, inst)

	local := childLocalKey(parent, inst)
	key := local
	if parent.isChild {
		key = parent.childKey + "-" + local
	}
	// Keys are field paths from the root, so an equal key anywhere in the tree
	// is the same field: the root's Child(&p.Stats.A) and Stats' Child(&s.A).
	root := parent
	for root.parent != nil {
		root = root.parent
	}
	if dup, _ := findUnit(root, key, false); dup != nil {
		panic(render.Miswired("via: " + unitTypeName(root.unitV.typ) + "." + strings.ReplaceAll(key, "-", ".") +
			" is passed to via.Child twice; a child has one container, so render it once"))
	}
	// Stamped onto the instance because a live child's push re-renders with
	// no parent in scope.
	inst.slotPrefix = parent.scopePrefix() + childSlotName(local) + "__"
	child, first := newChildCtx(parent, key, inst)
	parent.children = append(parent.children, child)

	// Only a request-scoped render inits, and only once per key: the child's
	// state lives in the parent's field, so a second OnInit in the same request
	// (a hydration pass, an action's response) or connection (a live root's
	// push) would run on state the first one already loaded.
	if parent.doInit {
		child.doInit = true
		if first {
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
		writeSignalsAttr(parent.logger(), &buf, child.initial)
		r.WriteString(buf.String())
	}
	r.WriteString(`>`)
	r.WriteString(string(child.rendered))
	r.WriteString(`</div>`)
}

// checkChildHooks warns about hooks the child type declares that never run,
// against the dedupe map of the parent's render kind.
func checkChildHooks(parent *Ctx, inst instance) {
	if parent.policy == bootPolicy {
		// A throwaway dedupe map: the boot render must not use up the warning
		// the first real render of this child owes the app.
		checkHooks(bootPolicy.log, inst.typ, &sync.Map{}, false)
	} else if parent.policy != nil {
		checkHooks(parent.logger(), inst.typ, parent.policy.hookWarned, false)
	} else {
		// A bare render has no Router, so nothing outlives it to dedupe against.
		checkHooks(slog.Default(), inst.typ, &sync.Map{}, false)
	}
}

// newChildCtx builds the child's Ctx from the parent's request scope, and
// reports whether it is the key's first in the request or connection (see
// reqScope.inited).
func newChildCtx(parent *Ctx, key string, inst instance) (*Ctx, bool) {
	child := newCtx()
	child.rev = parent.rev                         // a hydrated child must be revertable with its parent (see livePush)
	child.badDecodeLogged = parent.badDecodeLogged // one flag per tree: a malformed post hits every child's hydrator
	first := true
	if prior := parent.inited[key]; prior != nil {
		// prior's OnInit is also what registered the Tick or Listen making the
		// unit live, and a later render may not lower its verdict (I5).
		child.live = prior.live
		first = false
	} else if parent.inited != nil {
		parent.inited[key] = child
	}
	child.parent = parent
	child.isChild = true
	child.childKey = key
	child.unitV = inst
	child.base = parent.base // the mount prefix, so the child's own action URLs carry it too
	// A child is the parent of its own children, so it passes the declaration
	// down or a grandchild's slots never reach the client store.
	child.declare = parent.declare
	child.reqScope = parent.reqScope
	child.session = parent.session // one resolved session per request tree — see inheritRequestScope
	return child, first
}

// initOutcome is the sentinel initChild panics with: the render is already deep
// inside the parent's View with no return path, so the transport's recover
// turns it back into the answer OnInit asked for (see recoverToHTTP).
type initOutcome struct {
	err      error
	redirect redirectTo
}

// initChild runs a child's OnInit before its View. A paramMiss panic
// propagates untouched — the transport already answers it 404.
func initChild(child *Ctx, v any) {
	startTracks(child, child.unitV)
	ic, ok := v.(initer)
	if !ok {
		return
	}
	defer child.endInit() // see runOnInit
	child.inInit = true
	err := ic.OnInit(child)
	if err != nil {
		panic(initOutcome{err: err})
	}
	if child.redirect.url != "" {
		panic(initOutcome{redirect: child.redirect})
	}
}

// renderChildInner renders the child's View with child as the binder, so its
// actions and signals bind into its own tables. Returns the inner HTML only.
func renderChildInner(child *Ctx, v viewer) []byte {
	prebindSignals(child, child.unitV)
	child.viewRan = true
	rr := render.NewRenderer(binderCtx{child})
	rr.Render(v.View())
	return rr.Bytes()
}

// renderChildBind re-renders the child at key (no hydration, so it reflects
// post-action state) and returns its bind Ctx alongside the inner HTML. The Ctx
// is what lets a plain action's response ship a Signal.Set it wrote. Seeding it
// with the child's own key keeps a plain child's own Child keys composed
// exactly as the full-page walk composed them.
//
// underLive says whether c itself sits under a live ancestor in the real
// tree — false for a live child's own push (its ancestor chain was already
// proven plain, or it could never have become live), and whatever the
// discovery render found for a plain action's re-render, since that unit may
// legitimately sit beneath an already-live root (see checkLiveUnderLive).
func renderChildBind(key string, inst instance, r rerender, underLive bool) (*Ctx, []byte) {
	c := newCtx()
	c.isChild = true
	c.childKey = key
	c.unitV = inst
	c.base = r.base
	r.wire(c)
	body := renderChildInner(c, inst.v)
	checkLiveUnderLive(c, underLive)
	return c, body
}
