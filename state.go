package via

import (
	"fmt"
	"slices"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// State is server-authoritative, per-connection unit state. Unlike Signal it
// never reaches the client as a signal: its value is server-rendered as literal
// text and element-patched (morphed) into the live DOM when it changes — server
// owns the truth. Rendering one is what MAKES a unit live: a page whose View
// displays a State opens an SSE stream and holds its instance for the life of
// the connection, with no hook to write at all.
type State[T any] struct{ val T }

// Get returns the current value.
func (s *State[T]) Get() T { return s.val }

// Set assigns the value on this unit instance. The change reaches the browser
// on the next push — a Tick re-render, an action response, or a stream flush.
func (s *State[T]) Set(v T) { s.val = v }

// Display renders the current value as literal, escaped server text — and
// marks the unit it renders in LIVE, so server-held state is enough on its own
// to earn a connection. A bare render with no unit behind it (no binder) just
// writes the text.
//
// Liveness must be render-invariant. The verdict is taken from the render that
// serves the page, and only a streaming page bootstraps an SSE stream — so a
// Display reached through a branch that is CLOSED at GET wires the page
// plain, and an action that later opens the branch leaves the tab demanding
// a connection it never opened. via fails that action loudly rather than
// letting every action after it 410. Render the State unconditionally (use
// via.When INSIDE the row, not around the Display), or register a Tick/Listen
// in OnInit so the page is live from the first paint.
func (s *State[T]) Display() h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		if ctx := ctxOf(r.Binder()); ctx != nil {
			ctx.live = true
		}
		r.WriteEscaped(fmt.Sprint(s.val))
	})
}

// List is server-authoritative slice state — the canonical shape for a chat
// log, a feed, or a todo list. It embeds State[[]E], so Get and Set remain the
// general door (len(l.Get()), l.Get()[i], l.Set(slices.Insert(...))); Append,
// Remove and Each are the common cases, spelled so the intent reads at the
// call site. Removing or reordering rows morphs by position unless each row
// carries a stable id, so give the row an h.ID(…) when the order can change.
// Like State, rendering one makes its unit live.
type List[E any] struct{ State[[]E] }

// Append adds v to the end of the list on the per-(tab,embed) instance.
func (l *List[E]) Append(v E) { l.Set(append(l.Get(), v)) }

// Remove deletes the element at i, shifting the rest left. It panics if i is
// out of range. The freed slot is zeroed, so a pointer element does not stay
// reachable through the backing array.
func (l *List[E]) Remove(i int) { l.Set(slices.Delete(l.Get(), i, i+1)) }

// Each renders row(item) for every element, in order — sugar over
// via.Each(l.Get(), row), and like State.Display it marks the unit live. Same
// morph caveats as Each: an append-only list morphs by position; give each row
// a stable id for reorder/delete.
func (l *List[E]) Each(row func(E) h.H) h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		if ctx := ctxOf(r.Binder()); ctx != nil {
			ctx.live = true
		}
		r.Render(Each(l.Get(), row))
	})
}
