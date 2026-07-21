package via_test

import (
	"net/http"
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
