package demo_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/demo"
)

// cardPage shows one demo file twice, which is the case the radio group name
// has to survive: two cards on one page may not share a group.
type cardPage struct{}

func (p *cardPage) View() h.H {
	return h.Div(
		demo.Card("Counter", h.P(h.Str("prose")), h.Div(h.Str("live")), "counter.go"),
		demo.Card("Counter again", h.P(), h.Div(), "counter.go"),
	)
}

// collidingPage titles two cards so that they slug identically; only the
// digest tells the groups apart.
type collidingPage struct{}

func (p *collidingPage) View() h.H {
	return h.Div(
		demo.Card("Run it", h.P(), h.Div(), "counter.go"),
		demo.Card("Run-it", h.P(), h.Div(), "counter.go"),
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

func mountCards(r *via.Router)     { via.Mount(r, "/", cardPage{}) }
func mountColliding(r *via.Router) { via.Mount(r, "/", collidingPage{}) }

var groupNames = regexp.MustCompile(`name="(tab-[^"]+)"`)

func TestCard_rendersThreeTabsPerCard(t *testing.T) {
	t.Parallel()

	body := render(t, mountCards)

	assert.Equal(t, 6, strings.Count(body, `type="radio"`),
		"static/site.css picks the checked tab with :nth-of-type(1..3) rules, so a card has exactly three")
}

func TestCard_groupsTheTabsInALabelledFieldset(t *testing.T) {
	t.Parallel()

	body := render(t, mountCards)

	assert.Contains(t, body, `<fieldset class="demo-tabs">`)
	assert.Contains(t, body, `<legend class="sr-only">Demo view</legend>`)
}

func TestCard_givesEachCardItsOwnRadioGroup(t *testing.T) {
	t.Parallel()

	body := render(t, mountCards)

	assert.Contains(t, body, `name="tab-counter-counter-`)
	assert.Contains(t, body, `name="tab-counter-again-counter-`)
}

func TestCard_separatesCardsWhoseTitlesSlugAlike(t *testing.T) {
	t.Parallel()

	body := render(t, mountColliding)

	seen := map[string]bool{}
	for _, m := range groupNames.FindAllStringSubmatch(body, -1) {
		seen[m[1]] = true
	}
	assert.Len(t, seen, 2, "two cards slugging to %q must still get their own group", "run-it-counter")
}

var inspectorToggleIDs = regexp.MustCompile(`<input type="checkbox" id="([^"]+)">`)

func TestCard_givesEachCardsInspectorToggleAUniqueLabelledID(t *testing.T) {
	t.Parallel()

	body := render(t, mountCards)

	assert.Equal(t, 2, strings.Count(body, `<input type="checkbox"`),
		"one inspector toggle per card")

	ids := inspectorToggleIDs.FindAllStringSubmatch(body, -1)
	require.Len(t, ids, 2)
	assert.NotEqual(t, ids[0][1], ids[1][1],
		"a hardcoded id would let one card's toggle drive another's inspector")

	for _, m := range ids {
		assert.Contains(t, body, `<label for="`+m[1]+`">Show inspector</label>`)
	}
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
