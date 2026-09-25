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
		demo.Card(h.H3(h.Str("Counter")), h.P(h.Str("prose")), h.Div(h.Str("live")), "counter.go"),
		demo.Card(h.H3(h.Str("Counter again")), h.P(), h.Div(), "counter.go"),
	)
}

type optionsPage struct{}

func (p *optionsPage) View() h.H {
	return demo.Card(h.H3(h.Str("Counter")), h.P(), h.Div(h.Str("live")), "counter.go",
		demo.WireOpen(),
		demo.Try(h.Str("Click + twice."), h.Str("Reload the page.")))
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

func TestCard_foldsSourceAndWireIntoClosedDetails(t *testing.T) {
	t.Parallel()

	body := render(t, mountCards)

	assert.Equal(t, 2, strings.Count(body, `<details class="demo-more">`), "each card's Source")
	assert.Equal(t, 2, strings.Count(body, `<details class="demo-more demo-wire">`), "each card's Wire")
	assert.Equal(t, 2, strings.Count(body, `<summary>Source</summary>`))
	assert.Equal(t, 2, strings.Count(body, `<summary>Wire</summary>`))
	assert.NotContains(t, body, ` open>`, "neither starts open")
}

func TestWireOpen_startsTheWirePaneOpen(t *testing.T) {
	t.Parallel()

	body := render(t, func(r *via.Router) { via.Mount(r, "/", optionsPage{}) })

	assert.Contains(t, body, `<details class="demo-more demo-wire" open><summary>Wire</summary>`)
	assert.Contains(t, body, `<details class="demo-more"><summary>Source</summary>`, "Source still starts closed")
}

func TestTry_listsThingsToDoUnderTheLiveDemo(t *testing.T) {
	t.Parallel()

	body := render(t, func(r *via.Router) { via.Mount(r, "/", optionsPage{}) })

	assert.Contains(t, body, `<div class="demo-live"><div>live</div></div><div class="demo-try"><p class="demo-try-title">Try this</p><ul><li>Click + twice.</li><li>Reload the page.</li></ul></div><details`)
}

func TestAssets_loadsTheWireScriptUnderTheSiteBase(t *testing.T) {
	t.Parallel()

	a := demo.Assets(func(p string) string { return "/v0.7" + p })

	assert.Equal(t, []via.Script{{Src: "/v0.7/static/inspector.js", Defer: true}}, a.Scripts)
}

func TestCard_keepsTheLiveDemoOutsideTheDetails(t *testing.T) {
	t.Parallel()

	body := render(t, mountCards)

	assert.Contains(t, body, `<div class="demo-live"><div>live</div></div><details`,
		"the running demo is always visible, above the folded panes")
	assert.NotContains(t, body, "demo-try", "no Try list unless the card asks for one")
}

func TestCard_keepsTheEmptyPaneInStepWithTheInspector(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile(filepath.Join("..", "static", "inspector.js"))
	require.NoError(t, err)

	body := render(t, mountCards)
	empty := "Use the demo to see its requests and patches here."

	assert.Contains(t, body, empty)
	assert.Contains(t, string(script), `"`+empty+`"`,
		"inspector.js restores this string when the log is cleared; card.go writes the first one")
}

func TestCard_putsACopyButtonAfterEveryCodeBlock(t *testing.T) {
	t.Parallel()

	body := render(t, mountCards)

	assert.Equal(t, 2, strings.Count(body, `</pre><button class="copy" type="button" aria-label="Copy code"`),
		"each card's Source pane is one code block with its button right after it")
	assert.Contains(t, body, `data-on:click="navigator.clipboard.writeText(el.parentElement?.querySelector(&#34;pre&#34;)?.textContent ?? &#34;&#34;).catch(() =&gt; {}); el.classList.toggle(&#34;copied&#34;, true)"`)
	assert.Contains(t, body, `data-on:click__debounce.1500ms="el.classList.toggle(&#34;copied&#34;, false)"`,
		"a second listener clears the feedback once clicks stop")
}
