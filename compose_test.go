package via_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
)

// listComp exercises the composition primitives: a keyed-ish list via Each, an
// eager If, and a lazy When. row/lazy are named method values (no closures at
// the via call site — the guarantee lint must stay green).
type listComp struct {
	items []string
	show  bool
}

func (c *listComp) row(s string) h.H { return h.Li(h.Str(s)) }
func (c *listComp) lazy() h.H        { return h.Str("lazybuilt") }
func (c *listComp) View() h.H {
	return h.Div(
		h.Ul(via.Each(c.items, c.row)),
		via.If(c.show, h.Str("shown")),
		via.When(c.show, c.lazy),
	)
}

// Each must render every item in order, in place (no wrapper element), so a
// row method that returns <li> lands directly inside the <ul>.
func TestEach_rendersEveryItemInOrderInPlace(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Register(listComp{items: []string{"a", "b", "c"}})).Get("/")
	assert.Contains(t, body, "<ul><li>a</li><li>b</li><li>c</li></ul>")
}

// If/When render their content only when the condition holds.
func TestIfAndWhen_renderContentOnlyWhenTrue(t *testing.T) {
	t.Parallel()
	_, on := vt.Serve(t, via.Register(listComp{show: true})).Get("/")
	assert.Contains(t, on, "shown", "If(true) must render its node")
	assert.Contains(t, on, "lazybuilt", "When(true) must render the built node")

	_, off := vt.Serve(t, via.Register(listComp{show: false})).Get("/")
	assert.NotContains(t, off, "shown", "If(false) must render nothing")
	assert.NotContains(t, off, "lazybuilt", "When(false) must render nothing")
}
