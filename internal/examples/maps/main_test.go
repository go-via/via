package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/plugins/maplibre"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The page must render without panicking: on.Click takes a bound method, so a
// per-city closure would panic at render time and 500 the page. This is the
// regression guard for that whole class of mistake.
func TestMaps_pageRendersWithMapAndPluginAssets(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithPlugins(maplibre.Plugin()), via.WithTestServer(&server))
	via.Mount[Page](app, "/")
	defer server.Close()

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	body, _ := io.ReadAll(resp.Body)
	html := string(body)

	assert.Contains(t, html, "maplibre-gl.js", "the MapLibre script must be injected")
	assert.Contains(t, html, "maplibre-gl.css", "the required CSS must be injected")
	assert.Contains(t, html, "new maplibregl.Map(", "the map must initialize on the page")
	assert.Contains(t, html, ".addLayer(", "the route layer must be declared at load")
}

func TestMaps_flyToCityPushesCameraOverSSE(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithPlugins(maplibre.Plugin()), via.WithTestServer(&server))
	via.Mount[Page](app, "/")
	defer server.Close()

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	defer cancel()

	// cityIdx 3 is Tokyo; the button sets the signal, the action reads it.
	require.Equal(t, http.StatusOK, tc.Action("FlyToCity").WithSignal("cityIdx", 3).Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, "flyTo", "139.69")
}
