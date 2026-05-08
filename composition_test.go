package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

type simpleCounter struct {
	Name string
}

func (c *simpleCounter) View(ctx *via.Ctx) h.H {
	return h.Div(h.Text(c.Name))
}

func TestMount_rendersComposition(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[simpleCounter](app, "/counter")
	defer server.Close()

	body := getBody(t, server, "/counter")
	assert.Contains(t, body, "<div>")
}

func TestMount_renders404OnUnknownRoute(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[simpleCounter](app, "/counter")
	defer server.Close()

	resp, err := http.Get(server.URL + "/unknown")
	defer func() { _ = resp.Body.Close() }()
	if err != nil {
		t.Fatal(err)
	}
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestMount_panicsOnMissingView(t *testing.T) {
	t.Parallel()

	type noView struct{}
	app := via.New()
	assert.Panics(t, func() {
		via.Mount[noView](app, "/test")
	})
}

type pathParamPage struct {
	UserID int    `path:"id"`
	Slug   string `path:"slug"`
}

func (p *pathParamPage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Span(h.Textf("user=%d", p.UserID)),
		h.Span(h.Textf("slug=%s", p.Slug)),
	)
}

func TestMount_decodesPathParamsIntoTaggedFields(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[pathParamPage](app, "/u/{id}/posts/{slug}")
	defer server.Close()

	body := getBody(t, server, "/u/42/posts/hello")
	assert.Contains(t, body, "user=42", "path param int decoded into typed field")
	assert.Contains(t, body, "slug=hello", "path param string decoded into typed field")
}

type missingParamPage struct {
	UserID int `path:"id"`
}

func (p *missingParamPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestMount_panicsWhenPathTagHasNoMatchingSegment(t *testing.T) {
	t.Parallel()

	app := via.New()
	assert.Panics(t, func() {
		via.Mount[missingParamPage](app, "/no-id-segment")
	})
}
