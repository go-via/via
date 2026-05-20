package via_test

import (
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
)

type broadcastPage struct{}

func (p *broadcastPage) View(ctx *via.Ctx) h.H { return h.Div() }

// openSSEStreams spins up n test clients on path and opens an SSE stream
// for each. Returns the per-client frame channels and a single cancel
// func that closes them all.
func openSSEStreams(t *testing.T, server *httptest.Server, path string, n int) (frames []<-chan string, cancel func()) {
	t.Helper()
	frames = make([]<-chan string, n)
	cancels := make([]func(), n)
	for i := range n {
		tc := vt.NewClient(t, server, path)
		frames[i], cancels[i] = tc.SSE()
	}
	// Brief pause so the SSE handshakes complete before broadcast fires.
	time.Sleep(20 * time.Millisecond)
	return frames, func() {
		for _, c := range cancels {
			c()
		}
	}
}

// awaitNeedleOnAll waits for needle to appear on every frames channel.
// Channels are buffered (cap 16) so serial waits are safe — frames that
// arrive on later channels while we're waiting on earlier ones queue up.
func awaitNeedleOnAll(t *testing.T, frames []<-chan string, needle string, timeout time.Duration) {
	t.Helper()
	for _, ch := range frames {
		vt.AwaitFrame(t, ch, timeout, needle)
	}
}

func TestBroadcast_pushesScriptToEveryLiveTab(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[broadcastPage](app, "/")
	defer server.Close()

	frames, cancel := openSSEStreams(t, server, "/", 3)
	defer cancel()

	const msg = `console.log("hello broadcast")`
	assert.Equal(t, 3, app.Broadcast(msg),
		"Broadcast should report the tab count it reached")
	awaitNeedleOnAll(t, frames, msg, 2*time.Second)
}

func TestBroadcastSignals_pushesPatchToEveryLiveTab(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[broadcastPage](app, "/")
	defer server.Close()

	frames, cancel := openSSEStreams(t, server, "/", 2)
	defer cancel()

	assert.Equal(t, 2, app.BroadcastSignals(map[string]any{
		"_systemNotice": "maintenance soon",
	}))
	awaitNeedleOnAll(t, frames, "maintenance soon", 2*time.Second)
}

func TestBroadcastSignals_emptyMapIsNoOp(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[broadcastPage](app, "/")
	defer server.Close()

	_ = vt.NewClient(t, server, "/")
	assert.Equal(t, 0, app.BroadcastSignals(nil),
		"nil map should be reported as 0 tabs")
	assert.Equal(t, 0, app.BroadcastSignals(map[string]any{}),
		"empty map should be reported as 0 tabs")
}

func TestBroadcast_emptyIsNoOp(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[broadcastPage](app, "/")
	defer server.Close()

	_ = vt.NewClient(t, server, "/")
	assert.Equal(t, 0, app.Broadcast(""),
		"empty script should be reported as 0 tabs")
}
