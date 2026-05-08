package picocss_test

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/picocss"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type emptyPage struct{}

func (e *emptyPage) View(ctx *via.Ctx) h.H { return h.Div() }

func renderPage(t *testing.T, opts ...picocss.PicoOption) string {
	t.Helper()
	if testing.Short() {
		t.Skip("plugin test reaches the picocss CDN; skipped under -short")
	}
	var server *httptest.Server
	app := via.New(via.WithPlugins(picocss.Plugin(opts...)), via.WithTestServer(&server))
	via.Mount[emptyPage](app, "/")
	t.Cleanup(server.Close)
	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return string(body)
}

func TestPicocss_initialSignalsIncludeThemeAndDarkMode(t *testing.T) {
	t.Parallel()
	body := renderPage(t)
	assert.Contains(t, body, "_picoTheme")
	assert.Contains(t, body, "_picoDarkMode")
}

func TestPicocss_attachesStylesheetLink(t *testing.T) {
	t.Parallel()
	body := renderPage(t)
	assert.Contains(t, body, `id="_picoThemeLink"`,
		"plugin must inject a <link id=\"_picoThemeLink\"> into the document head")
}

func TestPicocss_servesThemeCSS(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("plugin test reaches the picocss CDN; skipped under -short")
	}
	var server *httptest.Server
	app := via.New(
		via.WithPlugins(picocss.Plugin(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue}))),
		via.WithTestServer(&server),
	)
	_ = app
	defer server.Close()
	resp, err := server.Client().Get(server.URL + "/_plugins/picocss/theme/blue")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, 200, resp.StatusCode)
	assert.Equal(t, "text/css", resp.Header.Get("Content-Type"))
}
