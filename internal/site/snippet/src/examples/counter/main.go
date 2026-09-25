// Command counter is a server-rendered counter: a click POSTs an action that
// mutates server state, and via re-renders the fragment into the live DOM.
package main

import (
	"cmp"
	"log"
	"net/http"
	"os"
	"sync"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// Store is a plain app dependency, shared across every request and tab.
type Store struct {
	mu sync.Mutex
	n  int
}

func (s *Store) Value() int { s.mu.Lock(); defer s.mu.Unlock(); return s.n }
func (s *Store) Add(d int)  { s.mu.Lock(); defer s.mu.Unlock(); s.n += d }

// snippet:start model
// Counter holds the injected dependency, not reactive state.
type Counter struct{ count *Store }

func (c *Counter) Inc(ctx *via.Ctx) { c.count.Add(1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.count.Add(-1) }

func (c *Counter) View() h.H {
	return h.Div(
		h.H1(h.Str(c.count.Value())),
		h.Button(on.Click(c.Dec), h.Str("-")),
		h.Button(on.Click(c.Inc), h.Str("+")),
	)
}

// snippet:end

func main() {
	store := &Store{}
	http.Handle("/", via.Handler(Counter{count: store}))
	log.Fatal(http.ListenAndServe(cmp.Or(os.Getenv("VIA_ADDR"), ":8080"), nil))
}
