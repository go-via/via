package testing

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snippet:start room
type Room struct {
	bus  *topic.Topic[string]
	Last via.State[string]
}

func (r *Room) OnInit(ctx *via.Ctx) error    { ctx.Listen(r.bus, r.heard); return nil }
func (r *Room) heard(ctx *via.Ctx, s string) { r.Last.Set(s) }
func (r *Room) Wave(ctx *via.Ctx)            { r.bus.Publish("wave") }

func (r *Room) View() h.H {
	return h.Div(
		h.P(h.Str("last: "), r.Last.Display()),
		h.Button(on.Click(r.Wave), h.Str("wave")),
	)
}

func TestRoom_aPublishReachesEveryTab(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(Room{bus: topic.New[string]()}))
	alice, bob := app.Connect(), app.Connect()
	assert.NotEqual(t, alice.TabID(), bob.TabID())

	status, _ := app.Action(0).Over(alice).Fire()
	require.Equal(t, 204, status)

	alice.Await("last: wave")
	bob.Await("last: wave")
}

// snippet:end
