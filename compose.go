package via

import (
	"github.com/go-via/via/h"
	"github.com/go-via/via/internal/hcore"
)

// Each renders row(item) for every item, in order, in place — a row method
// returning <li> lands directly inside the surrounding <ul>. row is a named
// method value, never a closure at the call site.
//
// TRAP: Datastar morphs the re-rendered list BY POSITION, which is right for an
// append-only list but wrong for reorder or delete — give each row a stable id
// (h.RawAttr("id", …)) so the morph matches by id instead. Per-row actions use
// OnArg, carrying the row's own key with the click. Per-row SIGNALS are not
// supported: a Signal inside a slice element is outside the composition struct,
// so it has no field offset to name itself by and rendering it panics.
func Each[T any](items []T, row func(T) h.H) h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		for _, item := range items {
			r.Render(row(item))
		}
	})
}

// When renders build()'s result when cond is true, and does not call build
// otherwise — lazy, so a branch only valid when the condition holds (it reads a
// value present only when logged in) is never evaluated on the false path.
// build is a named method value, never a closure at the call site.
//
// cond decides what is DISPATCHABLE, not just what is drawn: a handler or OnArg
// value inside a closed branch is not bound and answers 410 (see OnArg for the
// full property). So gate on session or database state — a Bind()ed Signal is
// whatever the client last set it to, which makes it a fine switch for a
// disclosure the user controls and never an authorization check.
//
// A Bind()ed signal as cond has one limit on a PLAIN (streamless) page: the
// render that decides dispatchability runs before the POST body is applied, so
// signals and content inside the branch round-trip normally but a HANDLER that
// only exists inside it answers 410. Put it outside the branch, or go live.
func When(cond bool, build func() h.H) h.H {
	return hcore.Dyn(func(r *hcore.Renderer) {
		if cond {
			r.Render(build())
		}
	})
}
