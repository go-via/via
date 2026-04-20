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

	resp, err := http.Get(server.URL + "/counter")
	require.NoError(t, err)
	defer resp.Body.Close()

	buf, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(buf), "<div>")
}

func TestMount_rendersDivWithContent(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[simpleCounter](app, "/counter")
	defer server.Close()

	resp, err := http.Get(server.URL + "/counter")
	require.NoError(t, err)
	defer resp.Body.Close()

	buf, _ := io.ReadAll(resp.Body)
	assert.Contains(t, string(buf), "<div")
}

func TestMount_panicsOnMissingView(t *testing.T) {
	t.Parallel()

	type noView struct{}
	app := via.New()
	assert.Panics(t, func() {
		via.Mount[noView](app, "/test")
	})
}

func TestMount_registersRoute(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[simpleCounter](app, "/counter")
	defer server.Close()

	resp, err := http.Get(server.URL + "/counter")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestMount_404UnknownRoute(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[simpleCounter](app, "/counter")
	defer server.Close()

	resp, err := http.Get(server.URL + "/unknown")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}
