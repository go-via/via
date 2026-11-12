package content_test

import (
	"net/http"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/demos"
)

func TestHookLog_logsBothInitsAndTheConnect(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(demos.HookLog{}))
	status, body := app.Get("/")
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "OnInit     GET /")
	conn := app.Connect()
	frame := conn.Await("OnConnect")
	assert.Contains(t, frame, "OnInit     POST /_via/sse")

	status, _ = vt.Act(app, func(p *demos.HookLog) func(*via.Ctx) { return p.Act }).Over(conn).Fire()
	require.Equal(t, http.StatusNoContent, status)
	conn.Await("OnReload")
}

func TestRelay_carriesAPickToTheSibling(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(demos.Relay{}))
	conn := app.Connect()
	status, _ := vt.ActArg(app, func(p *demos.Relay) func(*via.Ctx, string) { return p.Picker.Pick }, "Rust").Over(conn).Fire()
	require.Equal(t, http.StatusNoContent, status)
	conn.Await("last pick: <strong>Rust</strong>")
}
