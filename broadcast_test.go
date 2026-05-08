package via_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type broadcastPage struct{}

func (p *broadcastPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestBroadcast_pushesScriptToEveryLiveTab(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[broadcastPage](app, "/")
	defer server.Close()

	tcs := []*viatest.Client{
		viatest.NewClient(t, server, "/"),
		viatest.NewClient(t, server, "/"),
		viatest.NewClient(t, server, "/"),
	}

	frames := make([]<-chan string, len(tcs))
	cancels := make([]func(), len(tcs))
	for i, tc := range tcs {
		f, c := tc.SSE(t)
		frames[i], cancels[i] = f, c
	}
	defer func() {
		for _, c := range cancels {
			c()
		}
	}()
	time.Sleep(20 * time.Millisecond)

	const msg = `console.log("hello broadcast")`
	got := app.Broadcast(msg)
	assert.Equal(t, 3, got, "Broadcast should report the tab count it reached")

	// Each tab's SSE stream eventually carries the script.
	deadline := time.After(2 * time.Second)
	seen := 0
	bufs := make([]strings.Builder, len(tcs))
	for seen < 3 {
		anyProgress := false
		for i := range frames {
			if bufs[i].String() != "" && strings.Contains(bufs[i].String(), msg) {
				continue
			}
			select {
			case f, ok := <-frames[i]:
				if !ok {
					t.Fatalf("SSE %d closed early; got %q", i, bufs[i].String())
				}
				bufs[i].WriteString(f)
				anyProgress = true
			default:
			}
		}
		seen = 0
		for i := range bufs {
			if strings.Contains(bufs[i].String(), msg) {
				seen++
			}
		}
		if !anyProgress {
			select {
			case <-deadline:
				t.Fatalf("only %d/3 tabs saw the broadcast within 2s", seen)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	require.Equal(t, 3, seen)
}

func TestBroadcast_emptyIsNoOp(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[broadcastPage](app, "/")
	defer server.Close()

	_ = viatest.NewClient(t, server, "/")
	assert.Equal(t, 0, app.Broadcast(""),
		"empty script should be reported as 0 tabs")
}
