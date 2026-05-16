package h_test

import (
	"slices"
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

func TestEachSeq_consumesGoIterators(t *testing.T) {
	t.Parallel()
	got := render(t, h.Ul(
		h.EachSeq(slices.Values([]string{"a", "b", "c"}), func(s string) h.H {
			return h.Li(h.Text(s))
		}),
	))
	assert.Equal(t, "<ul><li>a</li><li>b</li><li>c</li></ul>", got)
}

func TestEachSeq_nilSeqProducesNoOutput(t *testing.T) {
	t.Parallel()
	got := render(t, h.Ul(h.EachSeq[int](nil, func(int) h.H { return h.Li() })))
	assert.Equal(t, "<ul></ul>", got)
}

func TestEachSeq2_consumesIndexedIterators(t *testing.T) {
	t.Parallel()
	got := render(t, h.Ul(
		h.EachSeq2(slices.All([]string{"x", "y"}), func(i int, s string) h.H {
			return h.Li(h.Textf("%d:%s", i, s))
		}),
	))
	assert.Equal(t, "<ul><li>0:x</li><li>1:y</li></ul>", got)
}

func TestClasses_joinsAndSkipsEmpty(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Classes("btn", "", "primary")))
	assert.Equal(t, `<div class="btn primary"></div>`, got)
}

func TestClasses_emptyInputProducesNoAttribute(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Classes("", "")))
	assert.Equal(t, `<div></div>`, got)
}

func TestClassMap_includesOnlyTrueKeys(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.ClassMap(map[string]bool{
		"on":  true,
		"off": false,
	})))
	assert.Contains(t, got, `class="`)
	assert.Contains(t, got, `on`)
	assert.NotContains(t, got, `off`)
}

func TestClassMap_emitsKeysInSortedOrder(t *testing.T) {
	t.Parallel()
	// Run a few times — Go's map iteration order is randomised, so a
	// non-deterministic implementation would eventually produce a
	// different ordering and fail this test.
	for range 25 {
		got := render(t, h.Div(h.ClassMap(map[string]bool{
			"zebra": true,
			"alpha": true,
			"mango": true,
		})))
		assert.Contains(t, got, `class="alpha mango zebra"`,
			"ClassMap output must be stable across renders")
	}
}

func TestClassMap_allFalseProducesNoAttribute(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.ClassMap(map[string]bool{"x": false, "y": false})))
	assert.Equal(t, `<div></div>`, got,
		"if no key is true the class attribute must be omitted entirely")
}

func TestIfStr_returnsConditional(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "yes", h.IfStr(true, "yes"))
	assert.Equal(t, "", h.IfStr(false, "yes"))
}

func TestSwitch_rendersMatchingCase(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Switch("settings",
		h.Case("overview", h.P(h.Text("o"))),
		h.Case("settings", h.P(h.Text("s"))),
		h.Default(h.P(h.Text("d"))),
	)))
	assert.Equal(t, "<div><p>s</p></div>", got)
}

func TestSwitch_fallsBackToDefault(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Switch("missing",
		h.Case("a", h.P(h.Text("a"))),
		h.Default(h.P(h.Text("d"))),
	)))
	assert.Equal(t, "<div><p>d</p></div>", got)
}

func TestSwitch_noDefaultRendersNothing(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Switch("none",
		h.Case("a", h.P(h.Text("a"))),
	)))
	assert.Equal(t, "<div></div>", got)
}

func TestSwitch_typedKeysCompareEquality(t *testing.T) {
	t.Parallel()
	type kind int
	const (
		alpha kind = iota
		beta
	)
	got := render(t, h.Div(h.Switch(beta,
		h.Case(alpha, h.P(h.Text("α"))),
		h.Case(beta, h.P(h.Text("β"))),
	)))
	assert.Contains(t, got, "β")
}

func TestFragment_rendersChildrenWithoutWrapper(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Fragment(
		h.H2(h.Text("title")),
		h.Hr(),
	)))
	assert.Equal(t, "<div><h2>title</h2><hr></div>", got,
		"Fragment must inline its children — no wrapping element")
}

func TestFragment_skipsNilChildren(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Fragment(
		h.P(h.Text("a")),
		nil,
		h.P(h.Text("b")),
	)))
	assert.Equal(t, "<div><p>a</p><p>b</p></div>", got,
		"nil children must be tolerated so callers can use If/When inside Fragment")
}

func TestFragment_emptyRendersNothing(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Fragment()))
	assert.Equal(t, "<div></div>", got)
}

func TestDataInit_bareLiteralPercentIsNotMangled(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.DataInit("$progress = '100%'")))
	assert.Contains(t, got, "100%",
		"no-arg form must skip Sprintf so a bare % stays literal")
	assert.NotContains(t, got, "NOVERB",
		"Sprintf-when-no-args used to corrupt the expression")
}

func TestDataShow_formatArgsStillWork(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.DataShow("$count > %d", 0)))
	assert.Contains(t, got, "$count &gt; 0",
		"args path must still format normally")
}

func TestIf_rendersNodeOnlyWhenTrue(t *testing.T) {
	t.Parallel()
	on := render(t, h.Div(h.If(true, h.P(h.Text("yes")))))
	off := render(t, h.Div(h.If(false, h.P(h.Text("yes")))))
	assert.Equal(t, "<div><p>yes</p></div>", on)
	assert.Equal(t, "<div></div>", off,
		"false condition must drop the node entirely, not render any placeholder")
}

func TestIfElse_picksBranchEagerly(t *testing.T) {
	t.Parallel()
	on := render(t, h.Div(h.IfElse(true,
		h.P(h.Text("yes")),
		h.P(h.Text("no")),
	)))
	off := render(t, h.Div(h.IfElse(false,
		h.P(h.Text("yes")),
		h.P(h.Text("no")),
	)))
	assert.Equal(t, "<div><p>yes</p></div>", on)
	assert.Equal(t, "<div><p>no</p></div>", off)
}

func TestWhenElse_runsOnlyTheWinningBuilder(t *testing.T) {
	t.Parallel()

	thenCalls, elsCalls := 0, 0
	thenB := func() h.H { thenCalls++; return h.P(h.Text("then")) }
	elsB := func() h.H { elsCalls++; return h.P(h.Text("els")) }

	got := render(t, h.Div(h.WhenElse(true, thenB, elsB)))
	assert.Equal(t, "<div><p>then</p></div>", got)
	assert.Equal(t, 1, thenCalls)
	assert.Equal(t, 0, elsCalls, "else builder must not run when condition is true")

	got = render(t, h.Div(h.WhenElse(false, thenB, elsB)))
	assert.Equal(t, "<div><p>els</p></div>", got)
	assert.Equal(t, 1, thenCalls, "then builder must not re-run when condition is false")
	assert.Equal(t, 1, elsCalls)
}

func TestWhenElse_toleratesNilBuilders(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "<div></div>",
		render(t, h.Div(h.WhenElse(true, nil, func() h.H { return h.P() }))))
	assert.Equal(t, "<div></div>",
		render(t, h.Div(h.WhenElse(false, func() h.H { return h.P() }, nil))))
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
