package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/require"
)

// The whole pitch of this example: a message sent from one browser appears
// live in every other connected browser, with no per-example wiring — it falls
// out of the StateAppEvents projector's cross-session re-render fan-out. Prove
// it with two independent sessions (separate cookie jars).
func TestChat_messageFansOutAcrossSessions(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[Room](app, "/")
	defer server.Close()

	alice := vt.NewClient(t, server, "/")
	bob := vt.NewClient(t, server, "/") // a different session

	bobFrames, cancel := bob.SSEReady()
	defer cancel()

	require.Equal(t, http.StatusOK, alice.Action("Send").
		WithSignal("name", "alice").
		WithSignal("draft", "hello bob").Fire())

	vt.AwaitFrame(t, bobFrames, 2*time.Second, "alice: ", "hello bob")
}

// The event-sourced model must build the message list by folding the log:
// several messages from different senders accumulate in send order in a fresh
// reader's view — proving the fold appends (not replaces) and the projection is
// app-scoped.
func TestChat_messagesAccumulateInOrder(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[Room](app, "/")
	defer server.Close()

	alice := vt.NewClient(t, server, "/")
	require.Equal(t, http.StatusOK, alice.Action("Send").
		WithSignal("name", "alice").WithSignal("draft", "first").Fire())
	require.Equal(t, http.StatusOK, alice.Action("Send").
		WithSignal("name", "bob").WithSignal("draft", "second").Fire())

	// A brand-new reader sees both lines, in send order — the fold accumulated
	// them into the app-scoped projection.
	require.Eventually(t, func() bool {
		html := vt.NewClient(t, server, "/").HTML()
		fi := strings.Index(html, "first")
		si := strings.Index(html, "second")
		return fi >= 0 && si >= 0 && fi < si
	}, 2*time.Second, 20*time.Millisecond,
		"both messages must accumulate in the folded list, in send order")
}
