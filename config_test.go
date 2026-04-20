package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
)

func TestWithAddr_setsAddr(t *testing.T) {
	t.Parallel()
	app := via.New(via.WithAddr(":9999"))
	assert.Equal(t, ":9999", app.Config().Addr())
}

func TestWithTitle_setsTitle(t *testing.T) {
	t.Parallel()
	app := via.New(via.WithTitle("My App"))
	assert.Equal(t, "My App", app.Config().Title())
}

func TestWithLogLevel_setsLevel(t *testing.T) {
	t.Parallel()
	app := via.New(via.WithLogLevel(via.LogDebug))
	assert.Equal(t, via.LogDebug, app.Config().LogLevel())
}

func TestWithShutdownTimeout_setsTimeout(t *testing.T) {
	t.Parallel()
	app := via.New(via.WithShutdownTimeout(10 * time.Second))
	assert.Equal(t, 10*time.Second, app.Config().ShutdownTimeout())
}

func TestWithSessionTTL_setsTTL(t *testing.T) {
	t.Parallel()
	app := via.New(via.WithSessionTTL(15 * time.Minute))
	assert.Equal(t, 15*time.Minute, app.Config().SessionTTL())
}

func TestWithSSEHeartbeat_setsHeartbeat(t *testing.T) {
	t.Parallel()
	app := via.New(via.WithSSEHeartbeat(30 * time.Second))
	assert.Equal(t, 30*time.Second, app.Config().SSEHeartbeat())
}

func TestWithSecureCookies_setsFlag(t *testing.T) {
	t.Parallel()
	app := via.New(via.WithSecureCookies())
	assert.True(t, app.Config().SecureCookies())
}

func TestWithTestServer_createsServer(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	assert.NotNil(t, app)
	defer server.Close()

	resp, err := http.Get(server.URL + "/_datastar.js")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}
