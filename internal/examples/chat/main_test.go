package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/require"
)

// The whole pitch of this example: a message sent from one browser appears
// live in every other connected browser, with no per-example wiring — it
// falls out of StateApp's cross-session re-render fan-out. Prove it with two
// independent sessions (separate cookie jars).
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
