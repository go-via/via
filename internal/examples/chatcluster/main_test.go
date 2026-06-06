package main

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/require"
)

// The whole point of the cluster example: two app instances (two "nodes") that
// share one backplane converge. A message Sent against node A must show up in a
// reader connected to node B — that is the cross-node fan-out the backplane
// buys you, and the reason this example exists separately from the single-node
// chat. We use via.InMemory() shared between both apps to prove convergence
// hermetically, without standing up NATS.
func TestMessageConvergesAcrossNodesSharingABackplane(t *testing.T) {
	t.Parallel()

	shared := via.InMemory()

	var serverA, serverB *httptest.Server
	appA := via.New(via.WithTestServer(&serverA), via.WithBackplane(shared))
	via.Mount[Room](appA, "/")
	defer serverA.Close()

	appB := via.New(via.WithTestServer(&serverB), via.WithBackplane(shared))
	via.Mount[Room](appB, "/")
	defer serverB.Close()

	// Node B starts empty — so a later sighting of the message proves it
	// crossed the backplane, not that B was pre-populated or is secretly node A.
	require.NotContains(t, vt.NewClient(t, serverB, "/").HTML(), "hello from A",
		"node B must start without node A's message")

	alice := vt.NewClient(t, serverA, "/") // talks to node A
	require.Equal(t, http.StatusOK, alice.Action("Send").
		WithSignal("name", "alice").
		WithSignal("draft", "hello from A").Fire())

	// A fresh reader on node B eventually sees the line node A appended — node
	// B's projector tailed the shared log and folded it.
	require.Eventually(t, func() bool {
		return strings.Contains(vt.NewClient(t, serverB, "/").HTML(), "hello from A")
	}, 2*time.Second, 20*time.Millisecond,
		"a message sent on node A must converge to a reader on node B via the shared backplane")
}

// Within a single node the event-sourced model must still build the list by
// folding the log in send order — the same guarantee as the single-node chat,
// re-asserted here so the cluster variant can't silently regress it.
func TestMessagesAccumulateInOrderWithinANode(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server), via.WithBackplane(via.InMemory()))
	via.Mount[Room](app, "/")
	defer server.Close()

	alice := vt.NewClient(t, server, "/")
	require.Equal(t, http.StatusOK, alice.Action("Send").
		WithSignal("name", "alice").WithSignal("draft", "first").Fire())
	require.Equal(t, http.StatusOK, alice.Action("Send").
		WithSignal("name", "bob").WithSignal("draft", "second").Fire())

	require.Eventually(t, func() bool {
		html := vt.NewClient(t, server, "/").HTML()
		fi := strings.Index(html, "first")
		si := strings.Index(html, "second")
		return fi >= 0 && si >= 0 && fi < si
	}, 2*time.Second, 20*time.Millisecond,
		"both messages must accumulate in the folded list, in send order")
}

// The node banner is the example's teaching device: it tells you which instance
// served the page so you can watch state cross between them. The configured
// node name must actually reach the rendered view.
func TestViewShowsTheServingNodeName(t *testing.T) {
	// Not parallel: mutates the package-level node identity. (Go runs the
	// non-parallel tests to completion — restoring nodeName via the defer —
	// before any t.Parallel test resumes, so there is no race.)
	prev := nodeName
	defer func() { nodeName = prev }()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server), via.WithBackplane(via.InMemory()))
	via.Mount[Room](app, "/")
	defer server.Close()

	// Flip the identity between two renders: the banner must track the var, so
	// a hardcoded constant can't satisfy both assertions.
	nodeName = "alpha-1"
	require.Contains(t, vt.NewClient(t, server, "/").HTML(), "alpha-1",
		"the rendered view must show which node served it")

	nodeName = "beta-2"
	html := vt.NewClient(t, server, "/").HTML()
	require.Contains(t, html, "beta-2",
		"the banner must reflect the current node identity, not a constant")
	require.NotContains(t, html, "alpha-1",
		"the banner must not retain a stale node identity")
}

// The fold bounds the rendered list to the most recent window so a long-lived
// room can't grow every fan-out render without bound — and it must keep the
// NEWEST lines, in order, not the oldest.
func TestFoldKeepsOnlyTheMostRecentWindowInOrder(t *testing.T) {
	t.Parallel()

	// Append more events than the window holds, each tagged with its index.
	var acc []Message
	for i := 0; i < recentWindow+3; i++ {
		acc = Posted{}.Fold(acc, Posted{From: "u", Body: strconv.Itoa(i)})
	}

	require.Len(t, acc, recentWindow, "the list is capped at the recent window")
	require.Equal(t, "3", acc[0].Body, "oldest surviving line is event #3 (the first 3 were dropped)")
	require.Equal(t, strconv.Itoa(recentWindow+2), acc[len(acc)-1].Body, "newest line is the last appended")
}

// Node identity is pod-local config, not chat state: prefer an explicit
// NODE_NAME, fall back to the hostname, and only then to a constant — so a
// misconfigured deployment still renders something rather than an empty banner.
func TestNodeNameResolutionPrefersEnvThenHostnameThenDefault(t *testing.T) {
	t.Parallel()

	got := resolveNodeName(
		func(string) string { return "from-env" },
		func() (string, error) { return "from-host", nil },
	)
	require.Equal(t, "from-env", got, "an explicit NODE_NAME wins")

	got = resolveNodeName(
		func(string) string { return "" },
		func() (string, error) { return "from-host", nil },
	)
	require.Equal(t, "from-host", got, "with no NODE_NAME, fall back to the hostname")

	got = resolveNodeName(
		func(string) string { return "" },
		func() (string, error) { return "", http.ErrServerClosed },
	)
	require.Equal(t, "node", got, "with neither, fall back to a non-empty default")
}
