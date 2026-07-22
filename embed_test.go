package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// banner is a plain (stateless) composition embedded by a layout.
type banner struct{}

func (b *banner) View() h.H { return h.P(h.Str("BANNER")) }

// shell is a generic layout: it renders a frame and embeds whatever composition
// its Body field holds — plain struct-field composition, no wrapper type. This
// is the content projection via.Embed exists for: one shell composes with any
// page, and the field type names the content statically.
type shell[C any] struct{ Body C }

func (s *shell[C]) View() h.H { return h.Div(h.H1(h.Str("SHELL")), via.Embed(s.Body)) }

// An embedded child renders in place, inside the layout's frame, wired as a
// positional island container. Fails if Embed stops wiring the container or
// renders the child outside the frame.
func TestEmbed_projectsChildInPlace(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(shell[banner]{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "SHELL", "the layout frame renders")
	assert.Contains(t, body, "BANNER", "the embedded content renders inside the frame")
	assert.Contains(t, body, `id="via-i0"`, "embedded content is wired as a positional island container")
	assert.Less(t, strings.Index(body, "SHELL"), strings.Index(body, "BANNER"),
		"content is embedded in place, after the frame heading")
}

// liveShell is a NON-live layout (no OnConnect) whose Body field holds a LIVE
// island. The page must bootstrap its SSE stream and the embedded live island
// must push its own container and render its server State — proving plain
// struct-field composition rides the live multiplex machinery.
type liveShell struct{ Body beater }

func (s *liveShell) View() h.H { return h.Div(h.H1(h.Str("APP")), via.Embed(s.Body)) }

func TestEmbed_projectsLiveIsland(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Register(
		liveShell{Body: beater{label: "hb"}},
		via.WithSSEHeartbeat(50*time.Millisecond),
	))
	conn := app.Connect()

	conn.Await(`id="via-i0"`) // the embedded live island pushes its own container
	conn.Await("hb=")         // and renders its own server State through the field
}

// Embed panics when the child lacks a View() — a wrote-it-wrong error surfaces
// loudly at the first render, never as a silent blank region.
func TestEmbed_panicsWithoutView(t *testing.T) {
	t.Parallel()
	require.PanicsWithValue(t,
		"via: via.Embed(child) requires child to have a View() method",
		func() { via.Embed(struct{ X int }{}) },
	)
}

// nestHost is an island (itself embedded by a page) whose View tries to embed
// a LIVE child — the unsupported nesting the guard must refuse.
type nestHost struct{ Inner beater }

func (n *nestHost) View() h.H { return h.Div(via.Embed(n.Inner)) }

type nestPage struct{ Host nestHost }

func (p *nestPage) View() h.H { return h.Div(via.Embed(p.Host)) }

// A live island embedded inside another island panics at the first render.
// Island discovery is flat: a nested live child would get no stream wiring —
// no ticks, no pushes, orphaned signals — so via refuses it loudly instead of
// rendering a dead region. Fails if the guard is dropped or its message drifts.
func TestEmbed_panicsOnNestedLiveIsland(t *testing.T) {
	t.Parallel()
	handler := via.Register(nestPage{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	require.PanicsWithValue(t,
		"via: nested live islands are unsupported — embed the live child in the page's View, not inside another island",
		func() { handler.ServeHTTP(httptest.NewRecorder(), req) },
	)
}

// The guard is about LIVE children only: a plain (stateless) child embedded
// inside an island still renders in place. Fails if the guard over-reaches to
// all nesting.
type plainNestHost struct{ Inner banner }

func (n *plainNestHost) View() h.H { return h.Div(h.H2(h.Str("HOST")), via.Embed(n.Inner)) }

type plainNestPage struct{ Host plainNestHost }

func (p *plainNestPage) View() h.H { return h.Div(via.Embed(p.Host)) }

func TestEmbed_allowsNestedPlainChild(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(plainNestPage{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "HOST", "the island renders")
	assert.Contains(t, body, "BANNER", "the nested plain child renders in place")
}

// livePage is a LIVE root (implements OnConnect) whose View embeds a LIVE
// island — the other unsupported combination: a live root takes the legacy
// whole-page stream, so the embedded island would never be wired.
type livePage struct{ Inner beater }

func (p *livePage) View() h.H                { return h.Div(via.Embed(p.Inner)) }
func (p *livePage) OnConnect(*via.Ctx) error { return nil }

// A live page embedding a live island panics at the first render: the legacy
// whole-page stream never discovers embedded islands, and its pushes would
// clobber the child's container. Fails if the guard is dropped or its message
// drifts.
func TestEmbed_panicsOnLiveIslandInLivePage(t *testing.T) {
	t.Parallel()
	handler := via.Register(livePage{})
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	require.PanicsWithValue(t,
		"via: a live page cannot embed live islands — drop the page's OnConnect and let the islands stream, or fold the live child into the page itself",
		func() { handler.ServeHTTP(httptest.NewRecorder(), req) },
	)
}

// The guard is about LIVE children only: a live page may still embed a plain
// (stateless) child — the whole-page push re-renders it in place, which is its
// normal semantics. Fails if the guard over-reaches to all embeds.
type livePlainPage struct{ Inner banner }

func (p *livePlainPage) View() h.H                { return h.Div(h.H1(h.Str("LIVEPAGE")), via.Embed(p.Inner)) }
func (p *livePlainPage) OnConnect(*via.Ctx) error { return nil }

func TestEmbed_allowsPlainChildInLivePage(t *testing.T) {
	t.Parallel()
	_, body := do(t, serve(t, via.Register(livePlainPage{})), http.MethodGet, "/", "")

	assert.Contains(t, body, "LIVEPAGE", "the live page renders")
	assert.Contains(t, body, "BANNER", "the plain child renders in place")
}
