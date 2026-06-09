package via_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
)

// tickEvent is a minimal EventReducer: each event carries an increment and the
// fold sums them. Used to prove StateAppEvents[E, V] participates in the walker
// binding seam exactly like the other scope handles.
type tickEvent struct{ n int }

func (tickEvent) Fold(acc int, ev tickEvent) int { return acc + ev.n }

// A StateAppEvents field must be detected by the walker and bound to its wire
// key through the same Mount path as StateApp/StateSess — otherwise the handle
// can never address its log and the whole backplane is unreachable. Default
// key is the lowercase field name.
type evtKeyPage struct {
	Log via.StateAppEvents[tickEvent, int]
}

func (p *evtKeyPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Span(h.ID("k"), h.Text(p.Log.Key())))
}

func TestStateAppEventsKeyBindsThroughMount(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[evtKeyPage](app, "/")

	c := vt.NewClient(t, server, "/")
	assert.Contains(t, c.HTML(), `<span id="k">log</span>`,
		"StateAppEvents field must bind its default wire key (lowercase field name)")
}

// The `via:` tag must override the wire key for a log handle, same as every
// other scope handle — config-by-convention with an explicit escape hatch.
type evtTaggedPage struct {
	Log via.StateAppEvents[tickEvent, int] `via:"events"`
}

func (p *evtTaggedPage) View(ctx *via.CtxR) h.H {
	return h.Div(h.Span(h.ID("k"), h.Text(p.Log.Key())))
}

func TestStateAppEventsKeyHonorsViaTag(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[evtTaggedPage](app, "/")

	c := vt.NewClient(t, server, "/")
	assert.Contains(t, c.HTML(), `<span id="k">events</span>`,
		"StateAppEvents must honor the `via:` tag wire-key override")
}
