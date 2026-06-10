package via_test

import (
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
)

type strictPage struct {
	N via.Signal[int8]
}

func (p *strictPage) Noop(ctx *via.Ctx)      {}
func (p *strictPage) View(ctx *via.CtxR) h.H { return h.Div(on.Click(p.Noop)) }

// By default decode is best-effort: a client value that overflows the signal's
// narrower type is silently truncated and the action still runs. This is the
// documented contract — the strict mode below is the opt-in that rejects it.
func TestStrictDecode_offTruncatesSilently(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[strictPage](app, "/")

	c := vt.NewClient(t, server, "/")
	// 9999 does not fit int8; without strict decode the action must still run.
	assert.Equal(t, 200, c.Action("Noop").WithSignal("n", 9999).Fire(),
		"best-effort decode must not reject an overflowing value")
}

// WithStrictDecode turns a client value that overflows (or shape-mismatches)
// the signal's type into a surfaced action error instead of a silent truncation
// that corrupts server state — the typed Signal is only as safe as the decode.
func TestStrictDecode_onRejectsOverflow(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithStrictDecode())
	server := vt.Serve(t, app)
	via.Mount[strictPage](app, "/")

	c := vt.NewClient(t, server, "/")
	frames, cancel := c.SSEReady()
	defer cancel()

	c.Action("Noop").WithSignal("n", 9999).Fire()
	body := vt.AwaitFrame(t, frames, 2*time.Second, "overflow")
	assert.Contains(t, body, "n",
		"the strict-decode error must name the offending signal")
}

// A value that fits must pass cleanly under strict decode — strictness rejects
// only genuinely lossy input, never valid input.
func TestStrictDecode_onAcceptsInRangeValue(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithStrictDecode())
	server := vt.Serve(t, app)
	via.Mount[strictPage](app, "/")

	c := vt.NewClient(t, server, "/")
	assert.Equal(t, 200, c.Action("Noop").WithSignal("n", 42).Fire(),
		"an in-range value must decode without error under strict mode")
}
