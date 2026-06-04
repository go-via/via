package via_test

import (
	"context"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A backplane wired via WithBackplane must be gracefully drained when the App
// shuts down — otherwise its goroutines/connections outlive the server. After
// Shutdown the caller's own reference must observe the closed state.
func TestWithBackplaneIsDrainedOnShutdown(t *testing.T) {
	t.Parallel()

	bp := via.InMemory()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server), via.WithBackplane(bp))
	defer server.Close()

	require.NoError(t, app.Shutdown(context.Background()))

	_, err := bp.Append(context.Background(), "k", []byte("x"))
	assert.ErrorIs(t, err, via.ErrClosed,
		"App.Shutdown must Close the backplane wired via WithBackplane")
}

// Adding the backplane drain to Shutdown must not regress the default-app path:
// a plain via.New() (no WithBackplane) still shuts down cleanly. This guards the
// new Close() step; that the nil default actually resolves to a real InMemory
// backplane (rather than a tolerated nil) is verified once Read/Append on the
// handle exist (P1.1b) — it is not black-box observable at this slice.
func TestDefaultAppShutsDownCleanlyWithBackplaneDrain(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	defer server.Close()

	assert.NotPanics(t, func() {
		require.NoError(t, app.Shutdown(context.Background()))
	}, "a default app resolves nil to InMemory and drains it without panic")
}
