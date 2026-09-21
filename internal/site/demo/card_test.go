package demo_test

import (
	"io"
	"net/http"
	"net/http/httptest"
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

func render(t *testing.T) string {
	t.Helper()
	app := via.NewRouter()
	t.Cleanup(app.Close)
	via.Mount(app, "/", cardPage{})

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

func TestCard_rendersThreeTabsPerCard(t *testing.T) {
	t.Parallel()

	body := render(t)

	assert.Equal(t, 6, strings.Count(body, `type="radio"`),
		"static/site.css picks the checked tab with :nth-of-type(1..3) rules, so a card has exactly three")
}

func TestCard_givesEachCardItsOwnRadioGroup(t *testing.T) {
	t.Parallel()

	body := render(t)

	assert.Contains(t, body, `name="tab-counter-counter"`)
	assert.Contains(t, body, `name="tab-counter-again-counter"`)
}
