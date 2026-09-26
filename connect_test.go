package via_test

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
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

// boundInt binds one int signal; nothing else a connect body carries names a
// slot of it.
type boundInt struct {
	Q via.Signal[int]
	n via.State[int]
}

func (p *boundInt) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Hour, p.tick)
	return nil
}
func (p *boundInt) tick(*via.Ctx) { p.n.Set(p.n.Get() + 1) }
func (p *boundInt) View() h.H     { return h.Div(h.Input(p.Q.Bind()), p.n.Display()) }

func heapInUse() int64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc)
}

func TestConnect_keepsNoConnectBodyValueTheUnitCannotUse(t *testing.T) {
	// Sequential: it measures the heap. A real socket, not vt's in-memory one,
	// which buffers what the client wrote and would hide what via keeps.
	srv := httptest.NewServer(via.Handler(boundInt{}))
	t.Cleanup(srv.Close)
	tr := &http.Transport{MaxIdleConnsPerHost: 32}
	t.Cleanup(tr.CloseIdleConnections)
	c := &http.Client{Transport: tr}
	pad := strings.Repeat("x", 480<<10)
	// An unknown key, and a known int slot holding a string that cannot decode.
	body := `{"junk":"` + pad + `","q":"` + pad + `"}`

	before := heapInUse()
	for range 20 {
		lines, cancel := openStreamWithBody(t, srv, c, "/_via/sse", body)
		t.Cleanup(cancel)
		awaitTabID(t, lines)
	}
	grown := heapInUse() - before

	assert.Less(t, grown, int64(4<<20),
		"20 open streams must not each retain the ~1MiB connect body no unit can use")
}
