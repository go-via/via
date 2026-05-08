package via_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
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

func TestSignal_initFromTagAppearsInPageSignals(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalCounter](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `&#34;step&#34;:1`,
		"signal init=1 must appear as initial value in data-signals meta")
}

func TestSignal_bindRendersAttributeWithKey(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalCounter](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-bind="step"`,
		"Signal.Bind() must render data-bind with the wire key")
}

func TestSignal_textRendersDataTextSpan(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalCounter](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-text="$step"`)
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
	c := via.NewBoundCtx(page)

	require.False(t, page.Open.Get(c))
	via.Toggle(c, &page.Open)
	assert.True(t, page.Open.Get(c), "Toggle must flip false → true")
	via.Toggle(c, &page.Open)
	assert.False(t, page.Open.Get(c), "Toggle must flip back true → false")
}

func TestToggle_nilSignalIsNoOp(t *testing.T) {
	t.Parallel()
	assert.NotPanics(t, func() {
		via.Toggle(via.NewBoundCtx(&togglePage{}), nil)
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
	c := via.NewBoundCtx(page)

	via.Add(c, &page.Count, 3)
	assert.Equal(t, 13, page.Count.Get(c))
	via.Add(c, &page.Count, -5)
	assert.Equal(t, 8, page.Count.Get(c), "negative delta = decrement")
}

func TestAdd_floatSignalRespectsType(t *testing.T) {
	t.Parallel()

	page := &adderPage{}
	c := via.NewBoundCtx(page)

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
	c := via.NewBoundCtx(page)

	// Same helper, different storage flavor — Mutable[T] makes them
	// interchangeable for read/modify/write helpers.
	via.Add(c, &page.Hits, 7)
	via.Add(c, &page.Hits, -2)
	assert.Equal(t, 5, page.Hits.Get(c))
}

func TestToggle_acceptsStateAsWellAsSignal(t *testing.T) {
	t.Parallel()

	page := &stateAdderPage{}
	c := via.NewBoundCtx(page)

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

func mustBeWellFormedHTML(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, "<html") {
		t.Fatalf("expected <html> in body, got: %s", body)
	}
}

func newCounterPostBody(via_tab string, signals map[string]any) *bytes.Buffer {
	// Datastar reads signals from the JSON body for POST/SSE.
	out := `{"via_tab":"` + via_tab + `"`
	for k, v := range signals {
		out += `,"` + k + `":`
		switch x := v.(type) {
		case string:
			out += `"` + x + `"`
		case int:
			out += strings.TrimSpace(strings.ReplaceAll(formatInt(x), " ", ""))
		}
	}
	out += "}"
	return bytes.NewBufferString(out)
}

func formatInt(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
