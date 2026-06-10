package ui_test

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/go-via/viashowcase/internal/core"
	"github.com/stretchr/testify/require"
)

// probe is a minimal composition exercising ONLY the StateAppEvents path that
// the showcase relies on — no DB, no plugins. It mirrors how Host/Join declare
// the Votes log (direct field, `via:"votes"`) so it reproduces the exact
// projector convergence behaviour in isolation.
type probe struct {
	Votes via.StateAppEvents[core.Vote, core.Tallies] `via:"votes"`
	Draft via.SignalStr                               `via:"draft"`
}

func (p *probe) Cast(ctx *via.Ctx) error {
	_, err := p.Votes.Append(ctx, core.Vote{Room: "r", Choice: strings.TrimSpace(p.Draft.Read(ctx)), By: "x"})
	return err
}

func (p *probe) View(ctx *via.CtxR) h.H {
	t := p.Votes.Read(ctx).For("r")
	return h.Div(h.ID("out"), h.Button(h.Text("cast"), on.Click(p.Cast)),
		h.Span(h.Textf("blue=%d", t["blue"])))
}

// A vote cast on app A must fold into app B's projection through one shared
// backplane — the exact property the host big-screen depends on.
func TestVoteFoldsIntoProjectionAcrossApps(t *testing.T) {
	t.Parallel()
	shared := via.InMemory()

	appA := via.New(via.WithBackplane(shared))
	srvA := vt.Serve(t, appA)
	via.Mount[probe](appA, "/")
	appB := via.New(via.WithBackplane(shared))
	srvB := vt.Serve(t, appB)
	via.Mount[probe](appB, "/")

	// Sanity: B starts with no votes.
	require.Contains(t, vt.NewClient(t, srvB, "/").HTML(), "blue=0")

	a := vt.NewClient(t, srvA, "/")
	require.Equal(t, http.StatusOK, a.Action("Cast").WithSignal("draft", "blue").Fire())

	// A fresh reader on app B must fold the vote into its projection.
	require.Eventually(t, func() bool {
		return strings.Contains(vt.NewClient(t, srvB, "/").HTML(), "blue=1")
	}, 2*time.Second, 20*time.Millisecond,
		"a vote cast on app A must fold into app B's projection via the shared backplane")
}

// Same property on a SINGLE app: the writer's own projector must fold its append
// (read-your-write is eventual but must land).
func TestVoteFoldsIntoProjectionSameApp(t *testing.T) {
	t.Parallel()
	app := via.New(via.WithBackplane(via.InMemory()))
	srv := vt.Serve(t, app)
	via.Mount[probe](app, "/")

	a := vt.NewClient(t, srv, "/")
	require.Equal(t, http.StatusOK, a.Action("Cast").WithSignal("draft", "blue").Fire())
	require.Eventually(t, func() bool {
		return strings.Contains(vt.NewClient(t, srv, "/").HTML(), "blue=1")
	}, 2*time.Second, 20*time.Millisecond, "the writer's projector must fold its own append")
}
