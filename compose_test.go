package via_test

import (
	"sync/atomic"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
)

// listComp exercises the composition primitives: a keyed-ish list via Each and
// a lazy When. row/lazy are named method values (no closures at the via call
// site — the guarantee lint must stay green). Via copies the root per request,
// so lazy's call count lives in a package-level atomic, not a struct field.
type listComp struct {
	items []string
	show  bool
}

var lazyBuildCalls atomic.Int32

func (c *listComp) row(s string) h.H { return h.Li(h.Str(s)) }
func (c *listComp) lazy() h.H        { lazyBuildCalls.Add(1); return h.Str("lazybuilt") }
func (c *listComp) View() h.H {
	return h.Div(
		h.Ul(via.Each(c.items, c.row)),
		via.When(c.show, c.lazy),
	)
}

func TestEach_rendersEveryItemInOrderInPlace(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Handler(listComp{items: []string{"a", "b", "c"}})).Get("/")
	assert.Contains(t, body, "<ul><li>a</li><li>b</li><li>c</li></ul>")
}

func TestWhen_rendersOnlyWhenTrueAndIsLazy(t *testing.T) {
	// Sequential, not Parallel: it reads the shared lazyBuildCalls counter.
	_, on := vt.Serve(t, via.Handler(listComp{show: true})).Get("/")
	assert.Contains(t, on, "lazybuilt", "When(true) must render the built node")

	before := lazyBuildCalls.Load()
	_, body := vt.Serve(t, via.Handler(listComp{show: false})).Get("/")
	assert.NotContains(t, body, "lazybuilt", "When(false) must render nothing")
	assert.Equal(t, before, lazyBuildCalls.Load(), "When(false) must not call build")
}
