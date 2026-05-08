package h_test

import (
	"strings"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func render(t *testing.T, n h.H) string {
	t.Helper()
	var buf strings.Builder
	if err := n.Render(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestEach_rendersOnePerItem(t *testing.T) {
	t.Parallel()
	got := render(t, h.Ul(
		h.Each([]string{"a", "b", "c"}, func(s string) h.H {
			return h.Li(h.Text(s))
		}),
	))
	assert.Equal(t, "<ul><li>a</li><li>b</li><li>c</li></ul>", got)
}

func TestEach_emptyListProducesNoOutput(t *testing.T) {
	t.Parallel()
	got := render(t, h.Ul(h.Each([]int{}, func(int) h.H { return h.Li() })))
	assert.Equal(t, "<ul></ul>", got)
}

func TestEachIndexed_passesIndex(t *testing.T) {
	t.Parallel()
	got := render(t, h.Ul(
		h.EachIndexed([]string{"x", "y"}, func(i int, s string) h.H {
			return h.Li(h.Textf("%d:%s", i, s))
		}),
	))
	assert.Equal(t, "<ul><li>0:x</li><li>1:y</li></ul>", got)
}

func TestWhen_buildsOnlyWhenTrue(t *testing.T) {
	t.Parallel()

	calls := 0
	build := func() h.H {
		calls++
		return h.P(h.Text("yes"))
	}

	on := render(t, h.Div(h.When(true, build)))
	assert.Contains(t, on, "<p>yes</p>")
	assert.Equal(t, 1, calls)

	off := render(t, h.Div(h.When(false, build)))
	assert.Equal(t, "<div></div>", off)
	assert.Equal(t, 1, calls, "build must not run when condition is false")
}
