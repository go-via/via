// Command counter is the slice-1 demo: a server-rendered, hypermedia counter.
// The count is authoritative server state held by an injected dependency
// (*Store); a click POSTs an action that mutates the store, and via re-renders
// the fragment and element-patches it into the live DOM. No client signal — the
// browser is just a rendering surface. The View has no '&', no identifier
// strings, and no closures at the call site.
package main

import (
	"cmp"
	"net/http"
	"os"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Store is the in-server state the counter tracks — a plain app dependency, not
// owned by via. Shared across every request/tab (it is a process singleton).
type Store struct {
	mu sync.Mutex
	n  int
}

func (s *Store) Value() int { s.mu.Lock(); defer s.mu.Unlock(); return s.n }
func (s *Store) Add(d int)  { s.mu.Lock(); s.n += d; s.mu.Unlock() }

// Counter holds the injected dependency, not reactive state. via passes a
// pointer to the instance per request, so the action method values (c.Inc,
// c.Dec) and the store pointer need no '&' at the call site.
type Counter struct{ count *Store }

func (c *Counter) Inc(ctx *via.Ctx) { c.count.Add(1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.count.Add(-1) }

// View is pure and ctx-free. It renders the dependency's current value straight
// into HTML; the post-action re-render reflects the mutated store and is
// element-patched into the page.
func (c *Counter) View() h.H {
	return h.Div(
		h.H1(h.Str(c.count.Value())),
		h.Button(via.OnClick(c.Dec), h.Str("-")),
		h.Button(via.OnClick(c.Inc), h.Str("+")),
	)
}

func main() {
	store := &Store{}
	http.Handle("/", via.Register(Counter{count: store}))
	http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil)
}
