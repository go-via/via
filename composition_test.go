package via_test

import (
	"net/http"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type simpleCounter struct {
	Name string
}

func (c *simpleCounter) View(ctx *via.CtxR) h.H {
	return h.Div(h.Text(c.Name))
}

func TestMount_rendersComposition(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[simpleCounter](app, "/counter")

	body := getBody(t, server, "/counter")
	assert.Contains(t, body, "<div>")
}

func TestMount_renders404OnUnknownRoute(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[simpleCounter](app, "/counter")

	resp, err := server.Client().Get(server.URL + "/unknown")
	defer func() { _ = resp.Body.Close() }()
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

type badOnInitPage struct{}

func (p *badOnInitPage) OnInit(ctx *via.Ctx) {} // missing error return

func (p *badOnInitPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestMount_panicShowsActualAndExpectedLifecycleSignature(t *testing.T) {
	t.Parallel()
	app := via.New()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "Mount[badOnInit] must panic")
		msg, _ := rec.(string)
		assert.Contains(t, msg, "OnInit has the wrong signature")
		assert.Contains(t, msg, "expected:")
		assert.Contains(t, msg, "got:")
		assert.Contains(t, msg, "func(*via_test.badOnInitPage, *via.Ctx)",
			"the 'got:' line must reflect the actual reflect.Type.String() output")
	}()
	via.Mount[badOnInitPage](app, "/")
}

type badViewReturnPage struct{}

func (p *badViewReturnPage) View(ctx *via.CtxR) int { return 0 } // wrong return type

func TestMount_panicsWhenViewReturnTypeIsNotAssignableToH(t *testing.T) {
	t.Parallel()
	// Mount should catch a View whose return type isn't assignable to
	// h.H, instead of letting the type-assert at render time blow up
	// on the first request with no breadcrumb to the bad composition.
	app := via.New()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "Mount with non-H View return must panic at registration time")
		msg, _ := rec.(string)
		assert.Contains(t, msg, "View has the wrong signature",
			"the panic must use the same shape as the param-shape panic")
		assert.Contains(t, msg, "badViewReturnPage",
			"the panic must name the offending type")
	}()
	via.Mount[badViewReturnPage](app, "/")
}

type badViewPage struct{}

func (p *badViewPage) View() h.H { return h.Div() } // missing *Ctx arg

func TestMount_panicShowsActualAndExpectedViewSignature(t *testing.T) {
	t.Parallel()
	app := via.New()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "Mount[badView] must panic")
		msg, _ := rec.(string)
		assert.Contains(t, msg, "expected:",
			"the panic must show the expected signature so users can read the contract")
		assert.Contains(t, msg, "got:",
			"the panic must show the actual signature so users can spot the diff")
		assert.Regexp(t, `func\(\*\S*badViewPage\) h\.H`, msg,
			"the 'got:' line must reflect the actual reflect.Type.String() output")
	}()
	via.Mount[badViewPage](app, "/")
}

func TestMount_panicsOnMissingView(t *testing.T) {
	t.Parallel()

	type noView struct{}
	app := via.New()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "Mount[noView] must panic")
		msg, _ := rec.(string)
		assert.Contains(t, msg, "missing required method",
			"panic must state that a required method is missing")
		assert.Contains(t, msg, "noView",
			"panic must name the offending type")
		assert.Contains(t, msg, "View(ctx *via.CtxR) h.H",
			"panic must show the expected method signature so the user can paste it in")
	}()
	via.Mount[noView](app, "/test")
}

type pathParamPage struct {
	UserID int    `path:"id"`
	Slug   string `path:"slug"`
}

func (p *pathParamPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		h.Span(h.Textf("user=%d", p.UserID)),
		h.Span(h.Textf("slug=%s", p.Slug)),
	)
}

func TestMount_decodesPathParamsIntoTaggedFields(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[pathParamPage](app, "/u/{id}/posts/{slug}")

	body := getBody(t, server, "/u/42/posts/hello")
	assert.Contains(t, body, "user=42", "path param int decoded into typed field")
	assert.Contains(t, body, "slug=hello", "path param string decoded into typed field")
}

type missingParamPage struct {
	UserID int `path:"id"`
}

func (p *missingParamPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestMount_panicsWhenPathTagHasNoMatchingSegment(t *testing.T) {
	t.Parallel()

	app := via.New()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "Mount with unmatched path tag must panic")
		msg, _ := rec.(string)
		assert.Contains(t, msg, `path:"id"`,
			"panic must name the offending tag so the user knows which field to fix")
		assert.Contains(t, msg, "/no-id-segment",
			"panic must echo the route so the user knows which Mount call site is wrong")
		assert.Contains(t, msg, "missingParamPage",
			"panic must name the composition type")
	}()
	via.Mount[missingParamPage](app, "/no-id-segment")
}

func TestMount_panicsWhenCisNotAStruct(t *testing.T) {
	t.Parallel()

	app := via.New()
	assert.PanicsWithValue(t,
		"via.Mount: C must be a struct, got int (kind: int)",
		func() { via.Mount[int](app, "/") },
		"non-struct type at C must surface a precise panic listing the offending type")
}

func TestMount_panicsWithTypeNameWhenCisAnInterface(t *testing.T) {
	t.Parallel()

	app := via.New()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "Mount[interface] must panic")
		msg, _ := rec.(string)
		assert.Contains(t, msg, "via.Composition",
			"the panic message must name the offending interface so the user can find the bad Mount call site")
		assert.Contains(t, msg, "concrete struct",
			"the panic message must explain what Mount expects instead")
	}()
	via.Mount[via.Composition](app, "/")
}

type typoOptionPage struct {
	Step via.SignalNum[int] `via:"step,initi=5"`
}

func (p *typoOptionPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestMount_panicsOnUnknownViaTagOption(t *testing.T) {
	t.Parallel()

	app := via.New()
	defer func() {
		rec := recover()
		require.NotNil(t, rec, "Mount with typo'd via-tag option must panic")
		msg, _ := rec.(string)
		assert.Contains(t, msg, "initi=5",
			"panic must echo the offending segment so the user can find the typo")
		assert.Contains(t, msg, "Step",
			"panic must name the offending field")
		assert.Contains(t, msg, "typoOptionPage",
			"panic must name the composition type")
	}()
	via.Mount[typoOptionPage](app, "/typo")
}
