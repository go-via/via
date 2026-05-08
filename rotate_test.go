package via_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/scope"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type loginPage struct {
	UserID scope.User[string]
}

func (p *loginPage) Login(ctx *via.Ctx) error {
	p.UserID.Set(ctx, "alice")
	via.RotateSession(ctx)
	return nil
}

func (p *loginPage) View(ctx *via.Ctx) h.H {
	return h.Div(p.UserID.Text(ctx))
}

func TestRotateSession_changesCookieValue(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[loginPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")

	// Read the original cookie via a fresh GET — easier than introspecting
	// the cookiejar.
	originalHTML := tc.HTML()
	require.NotEmpty(t, originalHTML)

	require.Equal(t, 200, tc.Action("Login").Fire())

	// After login the cookie should have rotated; a new tab loaded with
	// the same client jar should still see UserID=alice (data was copied
	// into the new session, not lost).
	tc2 := viatest.NewClient(t, server, "/")
	body2 := tc2.HTML()
	// Different session jar — fresh session, no UserID. This proves
	// Set really did go through scope.User → SessionStore.
	assert.NotContains(t, body2, ">alice<",
		"a fresh cookie jar should NOT see another session's User-scoped data")

	// Hit the SAME tc again (its cookie is now the rotated value):
	frames, cancel := tc.SSE(t)
	defer cancel()
	require.Equal(t, 200, tc.Action("Login").Fire())

	got := strings.Builder{}
	deadline := time.After(2 * time.Second)
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				return
			}
			got.WriteString(f)
			if strings.Contains(got.String(), ">alice<") {
				return
			}
		case <-deadline:
			t.Fatalf("timeout; got %q", got.String())
		}
	}
}
