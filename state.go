package via

import "github.com/go-via/via/v2/h"

// State is server-authoritative, per-connection island state. Unlike Signal it
// never reaches the client as a signal: its value is server-rendered as literal
// text and element-patched (morphed) into the live DOM when it changes — server
// owns the truth. It is valid only inside a live island's View (a composition
// that implements OnConnect); reading it on a stateless page panics at render.
type State[T any] struct{ val T }

// Get returns the current value.
func (s *State[T]) Get() T { return s.val }

// Set assigns the value on the island instance. The change reaches the browser
// on the next push — a Tick re-render, an action response, or a stream flush.
func (s *State[T]) Set(_ *Ctx, v T) { s.val = v }

// Display renders the current value as literal, escaped server text. It panics
// if rendered outside a live island: server-only state is meaningless on a
// stateless request/response page, and a silent zero value would hide the
// mistake. The guard fires on "this render is not an island render", so a live
// island's own first paint (before OnConnect) reads the constructor value fine.
func (s *State[T]) Display() h.H {
	return h.Dyn(func(r *h.Renderer) {
		ctx, ok := r.Binder().(*Ctx)
		if !ok || !ctx.island {
			panic("via: State[T] can only be read inside a live island's View — the composition must implement OnConnect")
		}
		r.WriteEscaped(sprint(s.val))
	})
}

// List is server-authoritative slice state with an Append one-liner — the
// canonical shape for a growing log (chat messages, a feed). It embeds
// State[[]E], so Get is inherited; render it with via.Each(l.Get(), row). Like
// State it is valid only inside a live island.
type List[E any] struct{ State[[]E] }

// Append adds v to the end of the list on the per-(tab,island) instance.
func (l *List[E]) Append(ctx *Ctx, v E) { l.Set(ctx, append(l.Get(), v)) }
