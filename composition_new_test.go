package via_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type helloComposition struct{}

func (helloComposition) View(ctx *via.Ctx) h.H {
	return h.Div(h.Text("Hello Via!"))
}

func TestMount_rendersComposition(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	app := via.New(via.WithTestServer(&srv))
	defer srv.Close()
	via.Mount[helloComposition](app, "/hello")

	resp, err := http.Get(srv.URL + "/hello")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "Hello Via!")
}

type userPage struct {
	ID   int    `path:"id"`
	Slug string `path:"slug"`
}

func (u *userPage) View(ctx *via.Ctx) h.H {
	return h.Div(h.Textf("id=%d slug=%s", u.ID, u.Slug))
}

func TestMount_decodesPathParams(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	app := via.New(via.WithTestServer(&srv))
	defer srv.Close()
	via.Mount[userPage](app, "/users/{id}/{slug}")

	resp, err := http.Get(srv.URL + "/users/42/alice")
	require.NoError(t, err)
	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Contains(t, string(body), "id=42 slug=alice")
}

type missingParamPage struct {
	ID int `path:"id"`
}

func (m *missingParamPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestMount_panicsWhenPathTagHasNoRouteSegment(t *testing.T) {
	t.Parallel()
	var srv *httptest.Server
	app := via.New(via.WithTestServer(&srv))
	defer srv.Close()

	assert.Panics(t, func() {
		via.Mount[missingParamPage](app, "/no-params")
	})
}
