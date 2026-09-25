package compositions_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/snippet/src/compositions"
)

func TestNames_matchTheComments(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		root http.Handler
		want []string
	}{
		{"one field of the type", via.Handler(compositions.Inbox{}), []string{`data-bind="chat__draft"`, `id="via-i0"`}},
		{"two fields of the type", via.Handler(compositions.Split{}), []string{
			`data-bind="i0__chat__draft"`, `data-bind="i1__chat__draft"`, `id="via-i0-0"`, `id="via-i1-0"`,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			status, body := vt.Serve(t, tt.root).Get("/")
			require.Equal(t, 200, status)
			for _, w := range tt.want {
				assert.Contains(t, body, w)
			}
		})
	}
}

func TestStepper_addsItsDefaultStep(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(compositions.Stepper{Label: "apples"}))
	conn := app.Connect()
	status, _ := app.Action(0).Over(conn).Fire()
	require.Equal(t, http.StatusNoContent, status)
	conn.Await("<output>1</output>")
}

func TestProfile_seedsTheChildInOnInit(t *testing.T) {
	t.Parallel()
	status, body := vt.Serve(t, via.Handler(compositions.Profile{Me: compositions.Avatar{Size: 48}})).Get("/")
	require.Equal(t, 200, status)
	assert.Contains(t, body, "ana<small>48px</small>")
}

func TestShell_rendersItsBodyAsAChild(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	t.Cleanup(r.Close)
	compositions.MountShells(r)
	app := vt.Serve(t, r)

	status, body := app.Get("/settings")
	require.Equal(t, 200, status)
	assert.Contains(t, body, "<title>Settings</title>")
	assert.Contains(t, body, `<main><div id="via-i0"`)
	assert.Contains(t, body, "volume")

	status, body = app.Get("/inbox")
	require.Equal(t, 200, status)
	assert.Contains(t, body, "ana")
	assert.Contains(t, body, `data-bind="body__main__chat__draft"`)
}

func TestTodos_rowsCarryTheirIDs(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(compositions.Todos{Items: via.ListOf(
		compositions.Todo{ID: 1, Title: "a"}, compositions.Todo{ID: 2, Title: "b"})}))
	status, body := app.Get("/")
	require.Equal(t, 200, status)
	assert.Contains(t, body, `<li id="todo-1">`)
	assert.Contains(t, body, `<li id="todo-2">`)

	conn := app.Connect()
	status, _ = app.Action(0).Over(conn).Fire()
	require.Equal(t, http.StatusNoContent, status)
	frame := conn.Await(`id="todo-2"`)
	assert.NotContains(t, frame, `id="todo-1"`)
}

func TestCart_passesTheTotalDown(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(compositions.Cart{}))
	conn := app.Connect()
	status, _ := app.Action(0).Over(conn).Fire()
	require.Equal(t, http.StatusNoContent, status)
	conn.Await("items: 1")
}

func TestBadge_readsTheMiddlewareValue(t *testing.T) {
	t.Parallel()
	r := via.Handler(compositions.Badge{})
	t.Cleanup(r.Close)
	srv := httptest.NewServer(compositions.WithUser(r))
	t.Cleanup(srv.Close)
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/", nil)
	require.NoError(t, err)
	req.Header.Set("X-User", "ana")
	resp, err := srv.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	var buf [1 << 16]byte
	n, _ := resp.Body.Read(buf[:])
	assert.Contains(t, string(buf[:n]), "<span>ana</span>")
}

func TestMenu_bindsPurgeOnlyForAnAdmin(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Handler(compositions.Menu{})).Get("/")
	assert.NotContains(t, body, "purge cache")
	_, body = vt.Serve(t, via.Handler(compositions.Menu{Admin: true})).Get("/")
	assert.Contains(t, body, "purge cache")
}

func TestOrder_derivesOnBothSides(t *testing.T) {
	t.Parallel()
	_, body := vt.Serve(t, via.Handler(compositions.Order{Price: 250})).Get("/")
	assert.Contains(t, body, `data-text="$qty * 250"`)
}

func TestClock_ticks(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(compositions.Clock{}))
	conn := app.Connect()
	conn.Await("datastar-patch-elements")
}
