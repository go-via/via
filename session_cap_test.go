package via_test

import (
	"net/http"
	"net/http/cookiejar"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Without a cap, a cookieless crawler can mint an unbounded number of sessions
// and OOM the pod. WithMaxSessions is the floor: once the live session map is
// full, a new cookieless request is refused with 503 rather than minting.
func TestMaxSessions_rejectsBeyondCap(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithMaxSessions(2))
	server := vt.Serve(t, app)
	via.Mount[maxCtxPage](app, "/")

	// server.Client() carries no cookie jar, so every request is a fresh,
	// cookieless mint — exactly the crawler-flood shape.
	for range 2 {
		resp, err := server.Client().Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"requests up to the cap must mint a session")
	}

	resp, err := server.Client().Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode,
		"a new session past the cap must be rejected with 503")
}

func TestMaxSessions_zeroDisablesTheCap(t *testing.T) {
	t.Parallel()

	app := via.New() // no WithMaxSessions
	server := vt.Serve(t, app)
	via.Mount[maxCtxPage](app, "/")

	for range 5 {
		resp, err := server.Client().Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"an unset cap must not reject any request")
	}
}

func TestMaxSessions_negativePanicsAtRegistration(t *testing.T) {
	t.Parallel()

	assert.PanicsWithValue(t, "via.WithMaxSessions: must be >= 0, got -1", func() {
		via.New(via.WithMaxSessions(-1))
	}, "a negative cap is a config error and must fail loudly at startup")
}

func TestMaxSessions_existingSessionPassesAtCap(t *testing.T) {
	t.Parallel()

	app := via.New(via.WithMaxSessions(1))
	server := vt.Serve(t, app)
	via.Mount[maxCtxPage](app, "/")

	// A client that keeps its cookie reuses its session and must never be
	// refused once it already holds one, even at cap=1.
	client := server.Client()
	cj, err := cookiejar.New(nil)
	require.NoError(t, err)
	client.Jar = cj
	for range 3 {
		resp, err := client.Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"a returning client must reuse its session, not re-mint")
	}
}
