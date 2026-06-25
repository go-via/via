package via

import "github.com/go-via/via/v2/h"

// Each renders row(item) for every item, in order, in place — a row method that
// returns <li> lands directly inside the surrounding <ul>, with no wrapper. row
// is a named method value (e.g. c.row), never a closure at the call site.
//
// Datastar morphs the re-rendered list by position, which is exactly right for
// an append-only list (the chat log): existing rows are untouched, a new one is
// appended. For reorder/delete identity, give each row a stable id in the row
// method (h.RawAttr("id", …)) so the morph matches by id.
//
// Action-index note: positional action ids stay stable as long as the rows
// carry no actions (the dominant list case). A list whose rows carry their own
// actions needs the structural-path cursor (see ROADMAP) so a growing list does
// not renumber a sibling action — that is deferred until a use case needs it.
func Each[T any](items []T, row func(T) h.H) h.H {
	return h.Dyn(func(r *h.Renderer) {
		for _, item := range items {
			r.Render(row(item))
		}
	})
}

// If renders node when cond is true, nothing otherwise. node is eager (already
// built), so it never trips the no-closure guarantee. Use it for conditional
// display; for an expensive or unsafe-when-false branch, use When.
func If(cond bool, node h.H) h.H {
	return h.Dyn(func(r *h.Renderer) {
		if cond {
			r.Render(node)
		}
	})
}

// When renders build()'s result when cond is true, and does not call build
// otherwise — for a branch that is expensive or only valid when the condition
// holds (e.g. reads a value present only when logged in). build is a named
// method value (e.g. c.adminPanel), never a closure at the call site.
func When(cond bool, build func() h.H) h.H {
	return h.Dyn(func(r *h.Renderer) {
		if cond {
			r.Render(build())
		}
	})
}
