package via

import (
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
		r.WriteEscaped(sprint(s.val))
	})
}

// List is server-authoritative slice state — the canonical shape for a chat
// log, a feed, or a todo list. It embeds State[[]E], so Get and Set are
// inherited and remain the general door; the methods below are the common
// slice edits, spelled so the intent reads at the call site:
//
//	l.Append(v)        l.Insert(i, v)     l.Replace(i, v)
//	l.Remove(i)        l.Truncate(n)      l.Clear()
//	l.Len()            l.At(i)
//
// Index arguments follow slice rules: an out-of-range index panics rather than
// silently doing nothing, because a wrong index is a programming error and a
// no-op would hide it. Render the list with via.Each(l.Get(), row). Removing or
// reordering rows morphs by position unless each row carries a stable id, so
// give the row a h.RawAttr("id", …) when the order can change. Like State it is
// valid only inside a live island.
type List[E any] struct{ State[[]E] }

// Len reports the number of elements.
func (l *List[E]) Len() int { return len(l.val) }

// At returns the element at i. It panics if i is out of range.
func (l *List[E]) At(i int) E { return l.val[i] }

// Append adds v to the end of the list on the per-(tab,island) instance.
func (l *List[E]) Append(v E) { l.Set(append(l.Get(), v)) }

// Insert places v at index i, shifting the rest right. i == Len appends. It
// panics if i is out of range.
func (l *List[E]) Insert(i int, v E) { l.Set(slices.Insert(l.Get(), i, v)) }

// Remove deletes the element at i, shifting the rest left. It panics if i is
// out of range. The freed slot is zeroed, so a pointer element does not stay
// reachable through the backing array.
func (l *List[E]) Remove(i int) { l.Set(slices.Delete(l.Get(), i, i+1)) }

// Replace overwrites the element at i. It panics if i is out of range.
func (l *List[E]) Replace(i int, v E) {
	s := l.Get()
	s[i] = v
	l.Set(s)
}

// Truncate keeps the first n elements and drops the rest, zeroing the dropped
// slots. It is a no-op if n is at least Len, and panics if n is negative.
func (l *List[E]) Truncate(n int) {
	if n < 0 {
		panic("via: List.Truncate: negative length")
	}
	if s := l.Get(); n < len(s) {
		l.Set(slices.Delete(s, n, len(s)))
	}
}

// Clear drops every element.
func (l *List[E]) Clear() { l.Set(nil) }
