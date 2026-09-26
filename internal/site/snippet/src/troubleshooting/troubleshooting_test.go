package troubleshooting

import (
	"bytes"
	"html"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuf) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func logTo(buf *syncBuf) via.Option { return via.WithLogger(slog.New(slog.NewTextHandler(buf, nil))) }

// postRe reads an action URL: a binding with a query ships it in the
// data-via-q-<event> attribute before its data-on, appended to the path.
var postRe = regexp.MustCompile(`(?:data-via-q-[^=\s]+="([^"]*)" data-on:[^=\s]+=")?@post\('([^']+)'`)

func postURLs(page string) []string {
	var out []string
	for _, m := range postRe.FindAllStringSubmatch(page, -1) {
		out = append(out, html.UnescapeString(m[2]+m[1]))
	}
	return out
}

type Gated struct {
	Open bool
	todo Todos
}

func (g *Gated) Hi(ctx *via.Ctx) {}
func (g *Gated) hi() h.H         { return h.Button(on.Click(g.Hi)) }
func (g *Gated) View() h.H       { return h.Div(g.todo.View(), via.When(g.Open, g.hi)) }

func TestGone_answersEachStaleCase(t *testing.T) {
	t.Parallel()
	seed := Todos{items: []Todo{{1, "a"}}}
	open := vt.Serve(t, via.Handler(Gated{Open: true, todo: seed}))
	_, page := open.Get("/")
	urls := postURLs(page)
	require.Len(t, urls, 2)

	buf := &syncBuf{}
	closed := vt.Serve(t, via.Handler(Gated{todo: seed}, logTo(buf)))

	status, body := closed.Action(0).Raw(urls[1]).Fire()
	assert.Equal(t, 410, status)
	assert.Contains(t, body, "; this render does not bind it")

	status, body = closed.Action(0).Raw(strings.Replace(urls[0], "a=1", "a=2", 1)).Fire()
	assert.Equal(t, 410, status)
	assert.Equal(t, "this render does not bind that action for that argument\n", body)

	status, body = closed.Action(0).Raw("/_via/a/3/x").Fire()
	assert.Equal(t, 410, status)
	assert.Equal(t, "no such child\n", body)

	assert.Contains(t, buf.String(), `msg="via: no such action; this render binds others"`)
	assert.Contains(t, buf.String(), `msg="via: the action is bound, but not for this arg"`)
}

type Board struct{ Msg via.State[string] }

func (b *Board) Post(ctx *via.Ctx) { b.Msg.Set("hello") }
func (b *Board) View() h.H         { return h.Div(b.Msg.Display(), h.Button(on.Click(b.Post))) }

func TestGone_liveActionWithoutItsStream(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(Board{}))

	status, body := app.Action(0).Fire()
	assert.Equal(t, 410, status)
	assert.Contains(t, body, "this action needs the page's tab id and the request carried no viatab signal")

	status, body = app.Action(0).Tab("deadbeef").Fire()
	assert.Equal(t, 410, status)
	assert.Contains(t, body, "this tab id has no open stream for this page")
}

func TestForbidden_originOffTheAllowlist(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(Board{}, via.WithTrustedOrigin("https://example.com")))

	status, body := app.Action(0).Origin("https://www.example.com").Fire()
	assert.Equal(t, 403, status)
	assert.Equal(t, "forbidden origin\n", body)

	status, _ = app.Action(0).NoOrigin().Fire()
	assert.Equal(t, 403, status)

	status, _ = app.Action(0).SecFetch("same-site").Origin("https://app.example.com").Fire()
	assert.Equal(t, 403, status)
}

func TestTooLarge_overMaxBody(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(Todos{items: []Todo{{1, "a"}}}, via.WithMaxBody(64)))

	status, body := app.Action(0).Body(`{"x":"` + strings.Repeat("a", 100) + `"}`).Fire()
	assert.Equal(t, 413, status)
	assert.Equal(t, "request body too large\n", body)
}

func TestUnavailable_pastMaxSSEConn(t *testing.T) {
	t.Parallel()
	buf := &syncBuf{}
	app := vt.Serve(t, via.Handler(Board{}, via.WithMaxSSEConn(1), logTo(buf)))
	c := app.Connect()
	defer c.Close()

	req, err := http.NewRequest(http.MethodPost, app.URL()+"/_via/sse", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("Datastar-Request", "true")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	assert.Equal(t, 503, resp.StatusCode)
	assert.Equal(t, "stream capacity reached\n", string(body))
	assert.Contains(t, buf.String(), "via: refusing an SSE connect with 503: the router is at its live-stream cap.")
}

type LiveParent struct {
	N     via.State[int]
	Clock Clock
}

func (p *LiveParent) View() h.H { return h.Div(p.N.Display(), via.Child(p.Clock)) }

type PtrParent struct{ Chat Chat }

func (p *PtrParent) View() h.H { return h.Div(via.Child(&p.Chat)) }

func TestRenderFailed_logsTheCompositionMistake(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		handler func(via.Option) http.Handler
		want    string
	}{
		{"live under live", func(o via.Option) http.Handler { return via.Handler(LiveParent{}, o) },
			"via: via.Child: a live unit cannot sit inside another live unit — embed it directly from a plain ancestor instead"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			buf := &syncBuf{}
			app := vt.Serve(t, tt.handler(logTo(buf)))
			status, body := app.Get("/")
			assert.Equal(t, 500, status)
			assert.Equal(t, "render failed\n", body)
			assert.Contains(t, buf.String(), `msg="via: render panic" err="`+tt.want+`"`)
		})
	}
}

func TestSiblings_liveChildrenUnderAPlainRoot(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(Dashboard{}))
	status, _ := app.Get("/")
	assert.Equal(t, 200, status)
}

type Gate struct {
	Show via.Signal[bool]
	Msg  via.State[string]
}

func (g *Gate) Open(ctx *via.Ctx) { g.Show.Set(true) }
func (g *Gate) msg() h.H          { return g.Msg.Display() }
func (g *Gate) View() h.H         { return h.Div(h.Button(on.Click(g.Open)), via.When(g.Show.Get(), g.msg)) }

func TestActionFailed_stateBehindAClosedBranch(t *testing.T) {
	t.Parallel()
	buf := &syncBuf{}
	app := vt.Serve(t, via.Handler(Gate{}, logTo(buf)))

	status, body := app.Action(0).Fire()
	assert.Equal(t, 500, status)
	assert.Equal(t, "action failed\n", body)
	assert.Contains(t, buf.String(), `msg="via: action panic" err="via: this action made a unit live on a page that was served plain — the tab has no stream.`)
}

type V07 struct{ N via.State[int] }

func (o *V07) OnInit(ctx *via.Ctx) error    { return nil }
func (o *V07) OnConnect(ctx *via.Ctx) error { return nil }
func (o *V07) OnDispose(ctx *via.Ctx)       {}
func (o *V07) View() h.H                    { return h.P(o.N.Display()) }

type Misnamed struct{ N via.State[int] }

func (o *Misnamed) Oninit(ctx *via.Ctx) error { return nil }
func (o *Misnamed) View() h.H                 { return h.P(o.N.Display()) }

func TestHooks_leftoverMethodsNeverRun(t *testing.T) {
	t.Parallel()
	buf := &syncBuf{}
	vt.Serve(t, via.Handler(V07{}, logTo(buf))).Get("/")
	assert.Contains(t, buf.String(), "via: troubleshooting.V07.OnConnect is shaped like the v0.7 OnConnect hook, which v0.8 never calls. "+
		"Move its work into OnInit: ctx.OnConnect(fn) runs fn once when the stream opens, and ctx.Tick or ctx.Listen makes the unit live.")
	assert.NotContains(t, buf.String(), "V07.OnDispose", "a v0.7 OnDispose has an action's shape, so it is silent")

	buf = &syncBuf{}
	vt.Serve(t, via.Handler(Misnamed{}, logTo(buf))).Get("/")
	assert.Contains(t, buf.String(), "via: troubleshooting.Misnamed.Oninit looks like a mis-named OnInit — it has the hook's "+
		"exact signature but troubleshooting.Misnamed has no OnInit func(*via.Ctx) error, so nothing will ever call it. Rename it to OnInit.")
}

type ValueView struct{ Q via.Signal[string] }

func (v ValueView) View() h.H { return h.Input(v.Q.Bind()) }

type NoErrInit struct{}

func (n *NoErrInit) OnInit(ctx *via.Ctx) {}
func (n *NoErrInit) View() h.H           { return h.P() }

func TestMount_panicsAtStartup(t *testing.T) {
	t.Parallel()
	assert.PanicsWithError(t, "via: troubleshooting.NoErrInit.OnInit has signature func(*via.Ctx), not func(*via.Ctx) error — so the hook will never run",
		func() { via.Handler(NoErrInit{}) })
	assert.PanicsWithError(t, "via: troubleshooting.ValueView.View has a VALUE receiver and the composition holds Signals — "+
		"View must take a POINTER receiver (func (p *ValueView) View() h.H), or every rendered Signal binds against a discarded copy",
		func() { via.Handler(ValueView{}) })
	assert.PanicsWithValue(t, `via: Mount path "/files/{path...}": a {name...} wildcard is not supported — a page's action and stream routes live under its path`,
		func() { via.Mount(via.NewRouter(), "/files/{path...}", NoErrInit{}) })
	assert.PanicsWithValue(t, "via: found at Mount, rendering *troubleshooting.PtrParent as mounted: "+
		"via: via.Child(child) requires child to have a View() method",
		func() { via.Handler(PtrParent{}) })
}
