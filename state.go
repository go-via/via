package via

import (
	"fmt"
	"slices"

	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

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
func (s *State[T]) Set(v T) { s.val = v }

// Display renders the current value as literal, escaped server text. It panics
// if rendered outside a live island: server-only state is meaningless on a
// stateless request/response page, and a silent zero value would hide the
// mistake. The guard fires on "this render is not an island render", so a live
// island's own first paint (before OnConnect) reads the constructor value fine.
func (s *State[T]) Display() h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		ctx := ctxOf(r.Binder())
		if ctx == nil || !ctx.island {
			panic("via: State[T] can only be read inside a live island's View — the composition must implement OnConnect")
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
// Like State it is valid only inside a live island.
type List[E any] struct{ State[[]E] }

// Append adds v to the end of the list on the per-(tab,island) instance.
func (l *List[E]) Append(v E) { l.Set(append(l.Get(), v)) }

// Remove deletes the element at i, shifting the rest left. It panics if i is
// out of range. The freed slot is zeroed, so a pointer element does not stay
// reachable through the backing array.
func (l *List[E]) Remove(i int) { l.Set(slices.Delete(l.Get(), i, i+1)) }

// Each renders row(item) for every element, in order — sugar over
// via.Each(l.Get(), row). Same morph caveats as Each: an append-only list morphs
// by position; give each row a stable id for reorder/delete.
func (l *List[E]) Each(row func(E) h.H) h.H { return Each(l.Get(), row) }
