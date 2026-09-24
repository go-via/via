package demo_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/demo"
)

type cardPage struct{}

func (p *cardPage) View() h.H {
	return h.Div(
		demo.Card("Counter", h.P(h.Str("prose")), h.Div(h.Str("live")), "counter.go"),
		demo.Card("Counter again", h.P(), h.Div(), "counter.go"),
	)
}

func render(t *testing.T, mount func(*via.Router)) string {
	t.Helper()
	app := via.NewRouter()
	t.Cleanup(app.Close)
	mount(app)

	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(body)
}

func mountCards(r *via.Router) { via.Mount(r, "/", cardPage{}) }

func TestCard_foldsSourceAndInspectorIntoClosedDetails(t *testing.T) {
	t.Parallel()

	body := render(t, mountCards)

	assert.Equal(t, 4, strings.Count(body, `<details class="demo-more">`),
		"each card has a Source and an Inspector, and neither starts open")
	assert.Equal(t, 2, strings.Count(body, `<summary>Source</summary>`))
	assert.Equal(t, 2, strings.Count(body, `<summary>Inspector</summary>`))
	assert.NotContains(t, body, `<details class="demo-more" open`)
}

func TestCard_keepsTheLiveDemoOutsideTheDetails(t *testing.T) {
	t.Parallel()

	body := render(t, mountCards)

	assert.Contains(t, body, `<div class="demo-live"><div>live</div></div><details`,
		"the running demo is always visible, above the folded panes")
}

func TestCard_keepsTheEmptyPaneInStepWithTheInspector(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile(filepath.Join("..", "static", "inspector.js"))
	require.NoError(t, err)

	body := render(t, mountCards)
	empty := "Interact with the demo to see requests, frames and signals here."

	assert.Contains(t, body, empty)
	assert.Contains(t, string(script), `"`+empty+`"`,
		"inspector.js restores this string when the log is cleared; card.go writes the first one")
}
