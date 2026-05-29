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
	if n == nil {
		return ""
	}
	var buf strings.Builder
	if err := n.Render(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.String()
}

func TestDiv_emitsOpenAndClose(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "<div></div>", render(t, h.Div()))
}

func TestVoidElement_skipsContentChildren(t *testing.T) {
	t.Parallel()
	got := render(t, h.Br(h.Text("nope")))
	assert.Equal(t, "<br>", got,
		"br is a void element — content children must be dropped")
}

func TestVoidElement_keepsAttributeChildren(t *testing.T) {
	t.Parallel()
	got := render(t, h.Img(h.Src("/x.png"), h.Text("dropped")))
	assert.Equal(t, `<img src="/x.png">`, got,
		"void element must still emit attributes")
}

func TestText_escapesEntities(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Text(`<x & "y" 'z'>`)))
	assert.Equal(t, `<div>&lt;x &amp; &#34;y&#34; &#39;z&#39;&gt;</div>`, got)
}

func TestText_emptyRendersNothing(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Text("")))
	assert.Equal(t, "<div></div>", got)
}

func TestRaw_passesThroughUnchanged(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Raw("<x>")))
	assert.Equal(t, "<div><x></div>", got)
}

func TestAttr_emittedInsideOpenTag(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.ID("a"), h.Class("b"), h.Text("x")))
	assert.Equal(t, `<div id="a" class="b">x</div>`, got)
}

func TestAttr_reorderedToOpenTag(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Text("x"), h.ID("a")))
	assert.Equal(t, `<div id="a">x</div>`, got,
		"attributes appearing after content must still emit inside the opening tag")
}

func TestAttr_valueIsEscaped(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Attr("title", `"oops"`)))
	assert.Equal(t, `<div title="&#34;oops&#34;"></div>`, got)
}

func TestAttr_booleanForm(t *testing.T) {
	t.Parallel()
	got := render(t, h.Input(h.Required()))
	assert.Equal(t, "<input required>", got)
}

func TestAttr_tooManyValuesPanics(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { _ = h.Attr("x", "a", "b") })
}

func TestData_prefixesAttrName(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Data("model", "$x")))
	assert.Equal(t, `<div data-model="$x"></div>`, got)
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
	assert.Contains(t, got, "on")
	assert.NotContains(t, got, "off")
}

func TestClassMap_emitsKeysInSortedOrder(t *testing.T) {
	t.Parallel()
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
	assert.Equal(t, `<div></div>`, got)
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
	assert.Equal(t, "<div><h2>title</h2><hr></div>", got)
}

func TestFragment_skipsNilChildren(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Fragment(
		h.P(h.Text("a")),
		nil,
		h.P(h.Text("b")),
	)))
	assert.Equal(t, "<div><p>a</p><p>b</p></div>", got)
}

func TestFragment_emptyRendersNothing(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Fragment()))
	assert.Equal(t, "<div></div>", got)
}

func TestFragment_attrInsideGroupBubblesToParent(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.Fragment(h.ID("x"), h.Text("body"))))
	assert.Equal(t, `<div id="x">body</div>`, got,
		"attribute inside a Fragment must still land in the parent's open tag")
}

func TestDataInit_bareLiteralPercentIsNotMangled(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.DataInit("$progress = '100%'")))
	assert.Contains(t, got, "100%")
	assert.NotContains(t, got, "NOVERB")
}

func TestDataShow_formatArgsStillWork(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.DataShow("$count > %d", 0)))
	assert.Contains(t, got, "$count &gt; 0")
}

func TestDataIgnoreMorph_emitsBooleanAttribute(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.DataIgnoreMorph()))
	assert.Equal(t, `<div data-ignore-morph></div>`, got,
		"DataIgnoreMorph is a name-only datastar attribute, not name=value")
}

func TestDataOnClick_emitsEscapedClickHandler(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.DataOnClick("@post('/inc')")))
	assert.Equal(t, `<div data-on:click="@post(&#39;/inc&#39;)"></div>`, got,
		"DataOnClick maps to the data-on:click attribute with an escaped value")
}

func TestDataClass_embedsClassNameAndFormatsExpression(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.DataClass("active", "$count > %d", 3)))
	assert.Equal(t, `<div data-class:active="$count &gt; 3"></div>`, got,
		"DataClass embeds the class in the suffix (data-class:active) and "+
			"forwards format args, escaping the result")
}

func TestIf_rendersNodeOnlyWhenTrue(t *testing.T) {
	t.Parallel()
	on := render(t, h.Div(h.If(true, h.P(h.Text("yes")))))
	off := render(t, h.Div(h.If(false, h.P(h.Text("yes")))))
	assert.Equal(t, "<div><p>yes</p></div>", on)
	assert.Equal(t, "<div></div>", off)
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
	assert.Equal(t, 0, elsCalls)

	got = render(t, h.Div(h.WhenElse(false, thenB, elsB)))
	assert.Equal(t, "<div><p>els</p></div>", got)
	assert.Equal(t, 1, thenCalls)
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
	build := func() h.H { calls++; return h.P(h.Text("yes")) }

	on := render(t, h.Div(h.When(true, build)))
	assert.Contains(t, on, "<p>yes</p>")
	assert.Equal(t, 1, calls)

	off := render(t, h.Div(h.When(false, build)))
	assert.Equal(t, "<div></div>", off)
	assert.Equal(t, 1, calls)
}

func TestHTML5_emitsFullDocument(t *testing.T) {
	t.Parallel()
	got := render(t, h.HTML5(h.HTML5Props{
		Title:       "T",
		Description: "D",
		Language:    "en",
		Head:        []h.H{h.Meta(h.Name("custom"))},
		Body:        []h.H{h.Div(h.Text("body"))},
	}))
	assert.True(t, strings.HasPrefix(got, "<!doctype html>"), got)
	assert.Contains(t, got, `<html lang="en">`)
	assert.Contains(t, got, `<meta charset="utf-8">`)
	assert.Contains(t, got, `<title>T</title>`)
	assert.Contains(t, got, `<meta name="description" content="D">`)
	assert.Contains(t, got, `<meta name="custom">`)
	assert.Contains(t, got, `<script type="module" src="/_datastar.js"></script>`)
	assert.Contains(t, got, "<div>body</div>")
}

func TestHTML5_omitsOptionalsWhenEmpty(t *testing.T) {
	t.Parallel()
	got := render(t, h.HTML5(h.HTML5Props{Title: "T"}))
	assert.Contains(t, got, "<title>T</title>")
	assert.NotContains(t, got, "lang=", "Language must be omitted when empty")
	assert.NotContains(t, got, "description", "Description meta must be omitted when empty")
}

func TestRawAttr_inlinesPreEscapedBytes(t *testing.T) {
	t.Parallel()
	got := render(t, h.Div(h.RawAttr([]byte(` data-x="1"`))))
	assert.Equal(t, `<div data-x="1"></div>`, got)
}

func TestElementConstructor_basicShapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  h.H
		want string
	}{
		{"u_emitsUnderline", h.U(h.T("x")), "<u>x</u>"},
		{"var_emitsVariable", h.Var(h.T("x")), "<var>x</var>"},
		{"video_emitsClosingTag", h.Video(h.Src("/v.mp4")), `<video src="/v.mp4"></video>`},
		{"wbr_emitsVoid", h.Wbr(), "<wbr>"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, render(t, tt.got))
		})
	}
}

func TestNewVoidTag_returnsReusableConstructor(t *testing.T) {
	t.Parallel()
	XInput := h.NewVoidTag("x-input")
	got := render(t, XInput(h.Attr("name", "field"), h.T("dropped")))
	assert.Equal(t, `<x-input name="field">`, got)
}

func TestFragment_topLevelSkipsAttributeFragments(t *testing.T) {
	t.Parallel()
	got := render(t, h.Fragment(
		h.ID("dropped"),
		h.P(h.T("kept")),
		h.Class("also-dropped"),
	))
	assert.Equal(t, "<p>kept</p>", got,
		"attribute fragments rendered at the top level must be skipped — "+
			"they would otherwise emit invalid HTML outside any element")
}
