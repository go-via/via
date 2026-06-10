package via_test

import (
	"errors"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
)

type boomPage struct{}

func (p *boomPage) Boom(ctx *via.Ctx)      { panic(errors.New("boom-detail-42")) }
func (p *boomPage) View(ctx *via.CtxR) h.H { return h.Div(on.Click(p.Boom)) }

// By default a recovered action panic must surface only a generic message to
// the browser — leaking the raw panic text (paths, query fragments, internal
// detail) to every client is an information-disclosure risk.
func TestActionPanic_defaultHidesRawMessageFromClient(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[boomPage](app, "/")

	c := vt.NewClient(t, server, "/")
	frames, cancel := c.SSEReady()
	defer cancel()

	c.Action("Boom").Fire()
	body := vt.AwaitFrame(t, frames, 2*time.Second, "Something went wrong")
	assert.NotContains(t, body, "boom-detail-42",
		"the raw panic message must not reach the client by default")
}

// In development, surfacing the real panic message to the browser is the
// fast-feedback the generic message destroys. WithVerboseErrors opts into it.
func TestActionPanic_verboseErrorsSurfacesRealMessage(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithVerboseErrors())
	server := vt.Serve(t, app)
	via.Mount[boomPage](app, "/")

	c := vt.NewClient(t, server, "/")
	frames, cancel := c.SSEReady()
	defer cancel()

	c.Action("Boom").Fire()
	vt.AwaitFrame(t, frames, 2*time.Second, "boom-detail-42")
}
