package via_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type numBoundsPage struct {
	N via.StateTabNum[int]
}

func (p *numBoundsPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Span(h.ID("n"), h.Textf("%d", p.N.Read(ctx))))
}

func (p *numBoundsPage) Set2(ctx *via.Ctx)         { p.N.Write(ctx, 2) }
func (p *numBoundsPage) Set50(ctx *via.Ctx)        { p.N.Write(ctx, 50) }
func (p *numBoundsPage) Set999(ctx *via.Ctx)       { p.N.Write(ctx, 999) }
func (p *numBoundsPage) AtLeast10(ctx *via.Ctx)    { p.N.Op(ctx).AtLeast(10) }
func (p *numBoundsPage) AtMost100(ctx *via.Ctx)    { p.N.Op(ctx).AtMost(100) }
func (p *numBoundsPage) Clamp10To100(ctx *via.Ctx) { p.N.Op(ctx).Clamp(10, 100) }

// numBoundsRun seeds the page via seed, applies verb, and returns the
// frame pushed by the verb's flush so the caller can assert the result.
func numBoundsRun(t *testing.T, seed, seedFrame, verb string) string {
	t.Helper()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[numBoundsPage](app, "/")

	tc := vt.NewClient(t, server, "/")
	frames, cancel := tc.SSEReady()
	t.Cleanup(cancel)

	require.Equal(t, http.StatusOK, tc.Action(seed).Fire())
	vt.AwaitFrame(t, frames, 2*time.Second, seedFrame)

	require.Equal(t, http.StatusOK, tc.Action(verb).Fire())
	return vt.AwaitFrame(t, frames, 2*time.Second, `<span id="n">`)
}

func TestNumOps_atLeastRaisesValueBelowFloor(t *testing.T) {
	t.Parallel()

	body := numBoundsRun(t, "Set2", `<span id="n">2</span>`, "AtLeast10")
	assert.Contains(t, body, `<span id="n">10</span>`,
		"AtLeast(10) must raise a value of 2 to the floor")
}

func TestNumOps_atMostLowersValueAboveCeiling(t *testing.T) {
	t.Parallel()

	body := numBoundsRun(t, "Set999", `<span id="n">999</span>`, "AtMost100")
	assert.Contains(t, body, `<span id="n">100</span>`,
		"AtMost(100) must lower a value of 999 to the ceiling")
}

func TestNumOps_boundVerbsConfineValueToRange(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		seed      string
		seedFrame string
		verb      string
		want      string
	}{
		{"AtLeast leaves in-range value untouched",
			"Set50", `<span id="n">50</span>`, "AtLeast10", `<span id="n">50</span>`},
		{"AtMost leaves in-range value untouched",
			"Set50", `<span id="n">50</span>`, "AtMost100", `<span id="n">50</span>`},
		{"Clamp raises value below floor",
			"Set2", `<span id="n">2</span>`, "Clamp10To100", `<span id="n">10</span>`},
		{"Clamp lowers value above ceiling",
			"Set999", `<span id="n">999</span>`, "Clamp10To100", `<span id="n">100</span>`},
		{"Clamp leaves in-range value untouched",
			"Set50", `<span id="n">50</span>`, "Clamp10To100", `<span id="n">50</span>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			body := numBoundsRun(t, tt.seed, tt.seedFrame, tt.verb)
			assert.Contains(t, body, tt.want)
		})
	}
}

func TestNumOps_clampPanicsOnInvertedBounds(t *testing.T) {
	t.Parallel()

	var s via.SignalNum[int]
	assert.PanicsWithValue(t,
		"via: Clamp called with inverted bounds (lo 10 > hi 1)",
		func() { s.Op(&via.Ctx{}).Clamp(10, 1) },
		"inverted bounds are a programming mistake — Clamp must fail at the call site, not silently reorder")
}
