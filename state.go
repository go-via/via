package via

import (
	"fmt"
	"slices"
	"unsafe"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
	"github.com/go-via/via/topic"
)

// State is server-authoritative, per-connection unit state. Unlike Signal it
// never reaches the client as a signal: it is server-rendered as literal text
// and morphed into the live DOM when it changes, with no client-side hook to
// write it at all. Rendering one makes its unit live.
//
// The zero State is ready to use, so a page normally leaves it zero and writes
// its starting value in OnInit with [State.Set]. Use [StateOf] instead when the
// starting value comes from outside the unit — a parent seeding an embedded
// child from a composite literal, where there is no OnInit of the child's to
// run. The two are the same job from opposite ends; pick by who owns the value.
//
// A constant start value can also be a field tag, `via:"init=<json>"`, read
// once when via walks the composition type at Mount. A [StateOf] literal wins
// over the tag, and a [State.Set] in OnInit wins over both.
//
// Not safe for concurrent use. Call it only from via callbacks (OnInit, an
// action handler, a Tick or Listen handler); to reach a unit from a goroutine
// of your own, publish to a [topic.Topic] the unit Listens to. See the package
// doc for the goroutine model.
//
// State has no Ref and reaches no client expression. When markup must react
// to it client-side, mirror it into a Signal with a Set in the same callback
// and use that Signal's Ref instead.
type State[T any] struct {
	val T
	// lit marks the start value as decided — by StateOf, by ListOf, or by the
	// one apply of a via:"init=…" tag — so a literal wins over the tag and no
	// later render re-seeds over what a Set has authored.
	lit   bool
	track *stateTracking[T] // set by StateTrack; nil on every other State
}

type stateTracking[T any] struct {
	topic *topic.Topic[T]
	load  func() T
}

// tracker is how a StateTrack literal is started without naming T: the type
// walk holds the handle as an any, so the closure it asks for here carries T
// out of the generic type, exactly as seedApplier does for a seed tag.
type tracker interface {
	trackStarter() func(handle unsafe.Pointer, ctx *Ctx)
}

func (*State[T]) trackStarter() func(unsafe.Pointer, *Ctx) {
	return func(handle unsafe.Pointer, ctx *Ctx) {
		s := (*State[T])(handle)
		if s.track == nil {
			return
		}
		s.Track(ctx, s.track.topic, s.track.load)
	}
}

// StateTrack is the literal form of [State.Track]: a State that seeds from
// load at every init of its unit, re-seeds once the stream has subscribed, and
// follows t.
//
//	via.Handler(Counter{Hits: via.StateTrack(room, n.Load)})
//
// load runs at each init, not here, so a request renders whatever the store
// holds then; the unit needs no OnInit of its own, and a literal on one that
// has an OnInit runs first so it reads the seeded value. Use it when the
// source is fixed at mount time; use Track in OnInit when it depends on the
// request.
func StateTrack[T any](t *topic.Topic[T], load func() T) State[T] {
	return State[T]{lit: true, track: &stateTracking[T]{topic: t, load: load}}
}

func (*State[T]) decodeSeed(raw string) (any, error) { return jsonSeed[T](raw) }

// seedApplier writes through the handle pointer the type walk found by offset.
// List[E] embeds State[[]E] first, so that pointer is the embedded State's
// address too and a List seeds through this same closure.
func (*State[T]) seedApplier(v any) func(unsafe.Pointer) any {
	seed := v.(T)
	return func(handle unsafe.Pointer) any {
		s := (*State[T])(handle)
		if !s.lit {
			s.lit, s.val = true, seed
		}
		return s.val
	}
}

// StateOf seeds a State with v, so a parent can hand an embedded child its
// starting value from a composite literal. A unit seeding its own state wants
// the zero [State] plus a [State.Set] in OnInit instead — that one can read the
// request, the path params and the session; a literal cannot:
//
//	type Page struct{ Chat Chat }
//	p := Page{Chat: Chat{Room: via.StateOf("lobby")}}
//
// The stored value is unexported (see the Field-Embeddable Types convention),
// so this constructor is the only way to write one outside a callback.
func StateOf[T any](v T) State[T] { return State[T]{val: v, lit: true} }

// Get returns this connection's value. It is server-authoritative: nothing the
// client sends can change it.
//
// Get does not make the unit live — only [State.Display] and [List.Each] do,
// because only a rendered State has anything to push. A unit that holds a State
// and merely Gets it (say, to build a string its View writes with h.Str) is a
// plain unit: it opens no stream, and a Set on it reaches no browser. Render
// the State with Display, or register a Tick/Listen in OnInit.
func (s *State[T]) Get() T { return s.val }

// Set assigns the value on this unit instance. The change reaches the browser
// on the next push — a Tick re-render, an action response, or a stream flush.
func (s *State[T]) Set(v T) { s.val = v }

// Track keeps s equal to a store announced on t: it seeds s from load now,
// seeds it again once the stream has subscribed, and then applies every
// publish. Valid only inside OnInit; a later call is ignored and logged. load
// may return one field of a larger store, in which case t carries that field's
// type.
func (s *State[T]) Track(ctx *Ctx, t *topic.Topic[T], load func() T) {
	if !ctx.inInit {
		// No partial seed: a one-shot copy that never follows is the trap this
		// whole method exists to remove.
		ctx.logger().Warn("via: Track called after OnInit returned — ignored; Track is valid only inside OnInit")
		return
	}
	s.Set(load())
	// The subscribe runs before any OnConnect fn, so this re-read covers every
	// write between OnInit and it.
	ctx.OnConnect(func() { s.Set(load()) })
	ctx.Listen(t, s.recv)
}

func (s *State[T]) recv(_ *Ctx, v T) { s.Set(v) }

// Display renders the current value as literal, escaped server text and marks
// its unit live, so server-held state alone earns a connection.
//
// trap: liveness must be render-invariant. Only the GET's verdict bootstraps an
// SSE stream, so a Display reached through a branch closed at GET wires the page
// plain, and an action that later opens the branch leaves the tab demanding a
// connection it never opened — via fails that action loudly rather than letting
// every action after it 410. Render the State unconditionally (put via.When
// inside the row, not around the Display), or register a Tick/Listen in OnInit.
func (s *State[T]) Display() h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		markLive(r)
		r.WriteEscaped(fmt.Sprint(s.val))
	})
}

// List is server-authoritative slice state — a chat log, a feed, a todo list.
// It embeds State[[]E], so Get and Set remain the general door
// (l.Set(slices.Insert(...))) and Append/Remove/Each spell the common cases.
// Track is promoted too: l.Track(ctx, t, load) in OnInit keeps the list equal
// to a store whose topic carries the whole []E.
// Rows morph by position unless each carries a stable id, so give the row an
// h.ID(…) when the order can change. Like State, rendering one makes its unit
// live, and like State it reads a `via:"init=<json>"` field tag —
// `via:"init=[\"a\",\"b\"]"` — that a [ListOf] literal wins over.
//
// Not safe for concurrent use — same rule as [State].
type List[E any] struct{ State[[]E] }

// ListOf seeds a List with the given elements — the [StateOf] of lists, and the
// same choice against a zero List filled in OnInit.
func ListOf[E any](v ...E) List[E] { return List[E]{State[[]E]{val: v, lit: true}} }

// Append adds v to the end of this connection's list and schedules the push,
// like any Set. It re-slices in place when there is capacity, so appending in a
// tick loop does not reallocate every frame — but the whole list re-renders on
// each push, so a list that grows without bound grows the frame without bound
// too. Cap it, or page it.
func (l *List[E]) Append(v E) { l.Set(append(l.Get(), v)) }

// Remove deletes the element at i, shifting the rest left, and panics if i is
// out of range. The freed slot is zeroed, so a pointer element does not stay
// reachable through the backing array.
func (l *List[E]) Remove(i int) { l.Set(slices.Delete(l.Get(), i, i+1)) }

// Each renders row(item) for every element, in order — sugar over
// via.Each(l.Get(), row), and like State.Display it marks the unit live. Same
// by-position morph trap as via.Each.
func (l *List[E]) Each(row func(E) h.H) h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		markLive(r)
		r.Render(Each(l.Get(), row))
	})
}

// markLive marks the rendering unit live: rendering server-authoritative state
// is itself what earns the unit a connection.
func markLive(r *hcore.Renderer) {
	if ctx := ctxOf(r.Binder()); ctx != nil {
		ctx.live = true
	}
}
