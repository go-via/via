package via

import (
	"fmt"
	"slices"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// State is server-authoritative, per-connection unit state. Unlike Signal it
// never reaches the client as a signal: it is server-rendered as literal text
// and morphed into the live DOM when it changes, with no client-side hook to
// write it at all. Rendering one MAKES its unit live.
//
// The zero State is ready to use, so a page normally leaves it zero and writes
// its starting value in OnInit with [State.Set]. Use [StateOf] instead when the
// starting value comes from OUTSIDE the unit — a parent seeding an embedded
// child from a composite literal, where there is no OnInit of the child's to
// run. The two are the same job from opposite ends; pick by who owns the value.
//
// NOT safe for concurrent use. Call it only from via callbacks (OnInit, an
// action handler, a Tick or Listen handler); to reach a unit from a goroutine
// of your own, publish to a [topic.Topic] the unit Listens to. See the package
// doc for the goroutine model.
type State[T any] struct{ val T }

// StateOf seeds a State with v, so a parent can hand an embedded child its
// starting value from a composite literal. A unit seeding its OWN state wants
// the zero [State] plus a [State.Set] in OnInit instead — that one can read the
// request, the path params and the session; a literal cannot:
//
//	type Page struct{ Chat Chat }
//	p := Page{Chat: Chat{Room: via.StateOf("lobby")}}
//
// The stored value is unexported (see the Field-Embeddable Types convention),
// so this constructor is the only way to write one outside a callback.
func StateOf[T any](v T) State[T] { return State[T]{val: v} }

// Get returns this connection's value. It is server-authoritative: nothing the
// client sends can change it.
//
// Get does NOT make the unit live — only [State.Display] and [List.Each] do,
// because only a rendered State has anything to push. A unit that holds a State
// and merely Gets it (say, to build a string its View writes with h.Str) is a
// PLAIN unit: it opens no stream, and a Set on it reaches no browser. Render
// the State with Display, or register a Tick/Listen in OnInit.
func (s *State[T]) Get() T { return s.val }

// Set assigns the value on this unit instance. The change reaches the browser
// on the next push — a Tick re-render, an action response, or a stream flush.
func (s *State[T]) Set(v T) { s.val = v }

// Display renders the current value as literal, escaped server text and marks
// its unit LIVE, so server-held state alone earns a connection.
//
// TRAP: liveness must be render-invariant. Only the GET's verdict bootstraps an
// SSE stream, so a Display reached through a branch CLOSED at GET wires the page
// plain, and an action that later opens the branch leaves the tab demanding a
// connection it never opened — via fails that action loudly rather than letting
// every action after it 410. Render the State unconditionally (put via.When
// INSIDE the row, not around the Display), or register a Tick/Listen in OnInit.
func (s *State[T]) Display() h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		markLive(r)
		r.WriteEscaped(fmt.Sprint(s.val))
	})
}

// List is server-authoritative slice state — a chat log, a feed, a todo list.
// It children State[[]E], so Get and Set remain the general door
// (l.Set(slices.Insert(...))) and Append/Remove/Each spell the common cases.
// Rows morph BY POSITION unless each carries a stable id, so give the row an
// h.ID(…) when the order can change. Like State, rendering one makes its unit
// live.
//
// NOT safe for concurrent use — same rule as [State].
type List[E any] struct{ State[[]E] }

// ListOf seeds a List with the given elements — the [StateOf] of lists, and the
// same choice against a zero List filled in OnInit.
func ListOf[E any](v ...E) List[E] { return List[E]{State[[]E]{val: v}} }

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

// markLive marks the rendering unit LIVE: rendering server-authoritative state
// is itself what earns the unit a connection.
func markLive(r *hcore.Renderer) {
	if ctx := ctxOf(r.Binder()); ctx != nil {
		ctx.live = true
	}
}
