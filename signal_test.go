package via_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type signalCounter struct {
	Step via.Signal[int] `via:"step,init=1"`
	Name via.Signal[string]
}

func (c *signalCounter) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Input(h.Type("number"), c.Step.Bind()),
		h.P(c.Step.Text()),
		h.Span(c.Name.Text()),
	)
}

func TestSignal_renderingProducesExpectedAttributes(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalCounter](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	cases := []struct {
		name, needle, why string
	}{
		{"init from tag", `&#34;step&#34;:1`, "init=1 must appear in data-signals meta"},
		{"Bind() renders data-bind", `data-bind="step"`, "Bind() must render data-bind with wire key"},
		{"Text() renders data-text span", `data-text="$step"`, "Text() must render data-text=$<key>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			assert.Contains(t, body, c.needle, c.why)
		})
	}
}

type togglePage struct {
	Open via.Signal[bool] `via:"open"`
}

func (p *togglePage) Flip(ctx *via.Ctx) error {
	via.Toggle(ctx, &p.Open)
	return nil
}

func (p *togglePage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestToggle_flipsBoolSignalAndMarksDirty(t *testing.T) {
	t.Parallel()

	page := &togglePage{}
	c := viatest.NewCtx(t, page)

	require.False(t, page.Open.Get(c))
	via.Toggle(c, &page.Open)
	assert.True(t, page.Open.Get(c), "Toggle must flip false → true")
	via.Toggle(c, &page.Open)
	assert.False(t, page.Open.Get(c), "Toggle must flip back true → false")
}

func TestToggle_nilSignalIsNoOp(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		via.Toggle(viatest.NewCtx(t, &togglePage{}), nil)
	})
}

type adderPage struct {
	Count via.Signal[int] `via:"count,init=10"`
	Bal   via.Signal[float64]
}

func (p *adderPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestAdd_intSignalAcceptsPositiveAndNegativeDeltas(t *testing.T) {
	t.Parallel()

	page := &adderPage{}
	c := viatest.NewCtx(t, page)

	via.Add(c, &page.Count, 3)
	assert.Equal(t, 13, page.Count.Get(c))
	via.Add(c, &page.Count, -5)
	assert.Equal(t, 8, page.Count.Get(c), "negative delta = decrement")
}

func TestAdd_nilMutableIsNoOp(t *testing.T) {
	t.Parallel()
	// Mirrors via.Toggle / via.SetIfChanged / via.Push: nil handle must
	// not panic. The constraint via.numeric forces T to a numeric kind,
	// so the test instantiates explicitly with [int].
	assert.NotPanics(t, func() {
		via.Add[int](viatest.NewCtx(t, &adderPage{}), nil, 1)
	})
}

func TestAdd_floatSignalRespectsType(t *testing.T) {
	t.Parallel()

	page := &adderPage{}
	c := viatest.NewCtx(t, page)

	via.Add(c, &page.Bal, 0.5)
	via.Add(c, &page.Bal, 0.25)
	assert.InDelta(t, 0.75, page.Bal.Get(c), 1e-9)
}

type stateAdderPage struct {
	Hits via.State[int]
	Open via.State[bool]
}

func (p *stateAdderPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestAdd_acceptsStateAsWellAsSignal(t *testing.T) {
	t.Parallel()

	page := &stateAdderPage{}
	c := viatest.NewCtx(t, page)

	// Same helper, different storage flavor — Mutable[T] makes them
	// interchangeable for read/modify/write helpers.
	via.Add(c, &page.Hits, 7)
	via.Add(c, &page.Hits, -2)
	assert.Equal(t, 5, page.Hits.Get(c))
}

func TestToggle_acceptsStateAsWellAsSignal(t *testing.T) {
	t.Parallel()

	page := &stateAdderPage{}
	c := viatest.NewCtx(t, page)

	via.Toggle(c, &page.Open)
	assert.True(t, page.Open.Get(c))
	via.Toggle(c, &page.Open)
	assert.False(t, page.Open.Get(c))
}

type signalShowPage struct {
	Open via.Signal[bool] `via:"open"`
}

func (p *signalShowPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.Open.Show(), h.Text("hello"))
}

func TestSignal_showRendersDataShowExpression(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalShowPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-show="$open"`,
		"Show should produce data-show=$<key>")
}

type fieldNameKey struct {
	MyField via.Signal[int]
}

func (c *fieldNameKey) View(ctx *via.Ctx) h.H { return h.Div() }

func TestSignal_keyDefaultsToLowercasedFieldName(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[fieldNameKey](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `&#34;myField&#34;:0`)
}

// helpers

func getBody(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	resp, err := http.Get(server.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return string(buf)
}

type feedPage struct {
	Series via.Signal[[]int] `via:"series"`
}

func (p *feedPage) Append(ctx *via.Ctx) error {
	via.Push(ctx, &p.Series, 42)
	return nil
}

func (p *feedPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestPush_appendsItemAndMarksSignalDirty(t *testing.T) {
	t.Parallel()
	p := &feedPage{}
	ctx := viatest.NewCtx(t, p)
	via.Push(ctx, &p.Series, 1)
	via.Push(ctx, &p.Series, 2)
	via.Push(ctx, &p.Series, 3)
	assert.Equal(t, []int{1, 2, 3}, p.Series.Get(ctx))
}

func TestPush_nilSignalIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := viatest.NewCtx(t, &feedPage{})
	// Must not panic; the nil-handle guard mirrors via.Toggle / via.Add.
	via.Push[int](ctx, nil, 1)
}

func TestPushBounded_dropsOldestWhenAtCapacity(t *testing.T) {
	t.Parallel()
	p := &feedPage{}
	ctx := viatest.NewCtx(t, p)
	for i := 1; i <= 5; i++ {
		via.PushBounded(ctx, &p.Series, i, 3)
	}
	assert.Equal(t, []int{3, 4, 5}, p.Series.Get(ctx),
		"PushBounded must keep the most recent max items; older items roll off")
}

func TestPushBounded_nilSignalIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := viatest.NewCtx(t, &feedPage{})
	// Mirrors TestPush_nilSignalIsNoOp; PushBounded's combined
	// `sig == nil || max <= 0` guard means each half needs its own pin.
	assert.NotPanics(t, func() {
		via.PushBounded[int](ctx, nil, 1, 10)
	})
}

func TestPushBounded_zeroMaxIsNoOp(t *testing.T) {
	t.Parallel()
	p := &feedPage{}
	ctx := viatest.NewCtx(t, p)
	via.PushBounded(ctx, &p.Series, 1, 0)
	assert.Empty(t, p.Series.Get(ctx))
}

type attrStylePage struct {
	Disabled via.Signal[bool]   `via:"disabled"`
	Hue      via.Signal[string] `via:"hue,init=blue"`
}

func (p *attrStylePage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Button(p.Disabled.Attr("disabled"), h.Text("Save")),
		h.Span(p.Hue.Style("color"), h.Text("hi")),
	)
}

func TestSignal_Attr_rendersDataAttrSyntax(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[attrStylePage](app, "/")
	defer server.Close()
	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-attr-disabled="$disabled"`,
		"Signal.Attr(name) should emit Datastar's data-attr-<name>=\"$key\"")
}

func TestSignal_Style_rendersDataStyleSyntax(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[attrStylePage](app, "/")
	defer server.Close()
	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-style-color="$hue"`,
		"Signal.Style(prop) should emit Datastar's data-style-<prop>=\"$key\"")
}

type stateIntInitPage struct {
	N via.State[int] `via:",init=3"`
}

func (p *stateIntInitPage) View(ctx *via.Ctx) h.H { return h.Div(p.N.Text()) }

func TestState_initTagSeedsNumericValueFromStructTag(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[stateIntInitPage](app, "/")
	defer server.Close()
	body := getBody(t, server, "/")
	assert.Contains(t, body, "<div>3</div>",
		"State[int] with init=3 must render the seeded value on first load")
}

type stateStringInitPage struct {
	Label via.State[string] `via:",init=--"`
}

func (p *stateStringInitPage) View(ctx *via.Ctx) h.H { return h.Div(p.Label.Text()) }

func TestState_initTagSeedsStringValueFromStructTag(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[stateStringInitPage](app, "/")
	defer server.Close()
	body := getBody(t, server, "/")
	assert.Contains(t, body, "<div>--</div>",
		"State[string] with init=-- must render the seeded value on first load")
}

type boolInitPage struct {
	On via.Signal[bool] `via:"on,init=true"`
}

func (p *boolInitPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestSignal_initTagParsesBoolFromStructTag(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[boolInitPage](app, "/")
	defer server.Close()
	body := getBody(t, server, "/")
	assert.Contains(t, body, `&#34;on&#34;:true`,
		"Signal[bool] with init=true must initialise to true (struct tags arrive as strings)")
}

type viewHelperPage struct {
	Step via.Signal[int]  `via:"step,init=1"`
	Open via.Signal[bool] `via:"open,init=false"`
}

func (p *viewHelperPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestSignal_viewHelpersRenderAllocFreeAfterBind(t *testing.T) {
	// AllocsPerRun forbids t.Parallel.
	// Drive bind via viatest.NewCtx so Signal.{Text,Bind,Show} have wire keys.
	p := &viewHelperPage{}
	_ = viatest.NewCtx(t, p)

	cases := []struct {
		name string
		node h.H
	}{
		{"Text", p.Step.Text()},
		{"Bind", p.Step.Bind()},
		{"Show", p.Open.Show()},
	}
	var buf bytes.Buffer
	for _, c := range cases {
		require.NoError(t, c.node.Render(&buf))
	}
	for _, c := range cases {
		allocs := testing.AllocsPerRun(50, func() {
			buf.Reset()
			_ = c.node.Render(&buf)
		})
		assert.Zero(t, allocs,
			"%s rendered output should be pre-baked bytes — Render must not allocate", c.name)
	}
}

type setIfChangedPage struct {
	Status via.Signal[string] `via:"status,init=idle"`
}

func (p *setIfChangedPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestSetIfChanged_skipsPatchWhenValueUnchanged(t *testing.T) {
	t.Parallel()
	p := &setIfChangedPage{}
	ctx := viatest.NewCtx(t, p)
	changed := via.SetIfChanged(ctx, &p.Status, "idle")
	assert.False(t, changed, "writing the existing value must report changed=false")
	ctx.Flush()
	assert.Empty(t, ctx.PendingSignals(),
		"unchanged Set must not enqueue a signal patch")
}

func TestSetIfChanged_marksDirtyWhenValueChanges(t *testing.T) {
	t.Parallel()
	p := &setIfChangedPage{}
	ctx := viatest.NewCtx(t, p)
	changed := via.SetIfChanged(ctx, &p.Status, "busy")
	assert.True(t, changed, "writing a new value must report changed=true")
	ctx.Flush()
	assert.Contains(t, ctx.PendingSignals(), "status",
		"changed Set must mark the signal dirty so the next flush patches it")
}

func TestSetIfChanged_nilMutableIsNoOp(t *testing.T) {
	t.Parallel()
	ctx := viatest.NewCtx(t, &setIfChangedPage{})
	changed := via.SetIfChanged[string](ctx, nil, "x")
	assert.False(t, changed)
}
