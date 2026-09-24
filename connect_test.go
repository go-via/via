package via_test

import (
	"bytes"
	"log"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

// tickSessionWriter starts a Tick in OnInit that writes to the session on
// every beat — the pattern I2 covers: a Tick/Listen handler's Ctx is the same
// Ctx OnInit held, so its sessW was set (for OnInit's own use) and, if
// never cleared, silently survives long past the point the response it
// pointed at was flushed.
type tickSessionWriter struct{ n via.State[int] }

func (t *tickSessionWriter) OnInit(ctx *via.Ctx) error {
	ctx.Tick(10*time.Millisecond, t.beat)
	return nil
}
func (t *tickSessionWriter) beat(ctx *via.Ctx) {
	t.n.Set(t.n.Get() + 1)
	ctx.Session().Put(member{Name: "orphan"})
}
func (t *tickSessionWriter) View() h.H { return h.Div(t.n.Display()) }

func TestConnectUnit_tickSessionWriteWarnsInsteadOfWritingADeadResponse(t *testing.T) {
	// Sequential: it captures the global log output.
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(tickSessionWriter{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
		lines, cancel := openStream(t, srv)
		defer cancel()
		_ = awaitTabID(t, lines)
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
	})

	assert.Contains(t, buf.String(), "no cookie can be set",
		"a Tick-minted session must warn instead of silently orphaning")
}

// tickSessionWriterOnConnectRead is tickSessionWriter with a read in OnInit,
// the README's recommended pattern: the handle cached there must not keep the
// connect writer live past the flush.
type tickSessionWriterOnConnectRead struct{ n via.State[int] }

func (t *tickSessionWriterOnConnectRead) OnInit(ctx *via.Ctx) error {
	ctx.Session().Get[member]()
	ctx.Tick(10*time.Millisecond, t.beat)
	return nil
}
func (t *tickSessionWriterOnConnectRead) beat(ctx *via.Ctx) {
	t.n.Set(t.n.Get() + 1)
	ctx.Session().Put(member{Name: "orphan"})
}
func (t *tickSessionWriterOnConnectRead) View() h.H { return h.Div(t.n.Display()) }

func TestConnectUnit_tickSessionWriteWarnsEvenAfterOnInitReadTheSession(t *testing.T) {
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(prev)

	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(tickSessionWriterOnConnectRead{}, via.WithSessionKey([]byte("a-test-signing-key-32-bytes-long"))))
		lines, cancel := openStream(t, srv)
		defer cancel()
		_ = awaitTabID(t, lines)
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
	})

	assert.Contains(t, buf.String(), "no cookie can be set",
		"OnInit's own read must not leave the cached handle's writer pointed at the flushed connect response")
}
