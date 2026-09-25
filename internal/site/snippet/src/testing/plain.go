// Package testing holds the compile-checked samples on /testing. Each Test
// function here is run by testing_test.go, so a sample that stops passing
// fails the site's go test.
package testing

import (
	"sync/atomic"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snippet:start counter
type Counter struct{ n *atomic.Int64 }

func (c *Counter) Inc(ctx *via.Ctx) { c.n.Add(1) }
func (c *Counter) Dec(ctx *via.Ctx) { c.n.Add(-1) }

func (c *Counter) View() h.H {
	return h.Div(
		h.Button(on.Click(c.Dec), h.Str("-")), // action 0
		h.P(h.Str("count: "), h.Str(c.n.Load())),
		h.Button(on.Click(c.Inc), h.Str("+")), // action 1
	)
}

// snippet:end

// snippet:start test
func TestCounter(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(Counter{n: new(atomic.Int64)}))

	status, body := app.Get("/")
	require.Equal(t, 200, status)
	assert.Contains(t, body, "count: 0")

	status, body = app.Action(1).Fire() // the second action on the page: Inc
	require.Equal(t, 200, status)
	assert.Contains(t, body, "count: 1") // the Datastar patch for the re-render
}

// snippet:end
