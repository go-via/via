package testing

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// snippet:start board
type Board struct{ Msg via.State[string] }

func (b *Board) Post(ctx *via.Ctx) { b.Msg.Set("hello") }

func (b *Board) View() h.H {
	return h.Div(
		h.P(h.Str("msg: "), b.Msg.Display()),
		h.Button(on.Click(b.Post), h.Str("post")),
	)
}

func TestBoard_pushesOverTheStream(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(Board{}))
	conn := app.Connect()

	status, _ := app.Action(0).Over(conn).Fire()
	require.Equal(t, 204, status) // a live action acks; the render rides the stream

	conn.Await("msg: hello")
}

// snippet:end

// snippet:start clock
type Clock struct{ n via.State[int] }

func (c *Clock) OnInit(ctx *via.Ctx) error { ctx.Tick(time.Minute, c.tick); return nil }
func (c *Clock) tick(ctx *via.Ctx)         { c.n.Set(c.n.Get() + 1) }
func (c *Clock) View() h.H                 { return h.P(h.Str("ticks="), c.n.Display()) }

func TestClock_ticksOncePerMinute(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(Clock{}))
		conn := app.Connect()

		time.Sleep(59 * time.Second)
		synctest.Wait()
		for {
			line, ok := conn.Peek()
			if !ok {
				break
			}
			assert.NotContains(t, line, "ticks=1")
		}

		time.Sleep(time.Second)
		conn.Await("ticks=1")
	})
}

// snippet:end

// snippet:start close
func TestBoard_shutdownEndsTheStreamCleanly(t *testing.T) {
	t.Parallel()
	r := via.Handler(Board{})
	app := vt.Serve(t, r)
	conn := app.Connect()

	r.Close()
	require.NoError(t, conn.AwaitClose())
}

// snippet:end
