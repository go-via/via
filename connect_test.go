package via_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/topic"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// tickSessionWriter starts a Tick in OnInit that writes to the session on
// every beat — the pattern I2 covers: a Tick/Listen handler's Ctx is the same
// Ctx OnInit held, so its sessW was set (for OnInit's own use) and, if
// never cleared, silently survives long past the point the response it
// pointed at was flushed.
type tickSessionWriter struct{ n via.State[int] }

func (t *tickSessionWriter) OnInit(ctx *via.Ctx) error {
	ctx.Tick(10*time.Millisecond, t.beat)
	return nil
}
func (t *tickSessionWriter) beat(ctx *via.Ctx) {
	t.n.Set(t.n.Get() + 1)
	ctx.Session().Put(member{Name: "orphan"})
}
func (t *tickSessionWriter) View() h.H { return h.Div(t.n.Display()) }

func TestConnectUnit_tickSessionWriteWarnsInsteadOfWritingADeadResponse(t *testing.T) {
	// sequential: swaps the global log writer
	buf := globalLog(t)

	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(tickSessionWriter{}, testSessionKey))
		lines, cancel := openStream(t, srv)
		defer cancel()
		_ = awaitTabID(t, lines)
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
	})

	assert.Contains(t, buf.String(), "no cookie can be set",
		"a Tick-minted session must warn instead of silently orphaning")
}

// tickSessionWriterOnConnectRead is tickSessionWriter with a read in OnInit:
// the handle cached there must not keep the connect writer live past the
// flush.
type tickSessionWriterOnConnectRead struct{ n via.State[int] }

func (t *tickSessionWriterOnConnectRead) OnInit(ctx *via.Ctx) error {
	ctx.Session().Get[member]()
	ctx.Tick(10*time.Millisecond, t.beat)
	return nil
}
func (t *tickSessionWriterOnConnectRead) beat(ctx *via.Ctx) {
	t.n.Set(t.n.Get() + 1)
	ctx.Session().Put(member{Name: "orphan"})
}
func (t *tickSessionWriterOnConnectRead) View() h.H { return h.Div(t.n.Display()) }

func TestConnectUnit_tickSessionWriteWarnsEvenAfterOnInitReadTheSession(t *testing.T) {
	// sequential: swaps the global log writer
	buf := globalLog(t)

	synctest.Test(t, func(t *testing.T) {
		srv := liveServer(t, via.Handler(tickSessionWriterOnConnectRead{}, testSessionKey))
		lines, cancel := openStream(t, srv)
		defer cancel()
		_ = awaitTabID(t, lines)
		time.Sleep(50 * time.Millisecond)
		synctest.Wait()
	})

	assert.Contains(t, buf.String(), "no cookie can be set",
		"OnInit's own read must not leave the cached handle's writer pointed at the flushed connect response")
}

// boundInt binds one int signal; nothing else a connect body carries names a
// slot of it.
type boundInt struct {
	Q via.Signal[int]
	n via.State[int]
}

func (p *boundInt) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Hour, p.tick)
	return nil
}
func (p *boundInt) tick(*via.Ctx) { p.n.Set(p.n.Get() + 1) }
func (p *boundInt) View() h.H     { return h.Div(h.Input(p.Q.Bind()), p.n.Display()) }

func heapInUse() int64 {
	runtime.GC()
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return int64(ms.HeapAlloc)
}

func TestConnect_keepsNoConnectBodyValueTheUnitCannotUse(t *testing.T) {
	// sequential: measures the heap. A real socket, not vt's in-memory one,
	// which buffers what the client wrote and would hide what via keeps.
	srv := httptest.NewServer(via.Handler(boundInt{}))
	t.Cleanup(srv.Close)
	tr := &http.Transport{MaxIdleConnsPerHost: 32}
	t.Cleanup(tr.CloseIdleConnections)
	c := &http.Client{Transport: tr}
	pad := strings.Repeat("x", 480<<10)
	// An unknown key, and a known int slot holding a string that cannot decode.
	body := `{"junk":"` + pad + `","q":"` + pad + `"}`

	before := heapInUse()
	for range 20 {
		lines, cancel := openStreamWithBody(t, srv, c, "/_via/sse", body)
		t.Cleanup(cancel)
		awaitTabID(t, lines)
	}
	grown := heapInUse() - before

	assert.Less(t, grown, int64(4<<20),
		"20 open streams must not each retain the ~1MiB connect body no unit can use")
}

// A plain root on a page that streams is held by the tab like a live root:
// its actions run on the tab's instance and its push carries its live
// children. These cover the paths a held root shares with them.

// heldKid is a live child: it follows a topic and counts its own clicks.
type heldKid struct {
	feed *topic.Topic[string]
	N    via.State[int]
	Last via.State[string]
}

func (k *heldKid) OnInit(ctx *via.Ctx) error { ctx.Listen(k.feed, k.recv); return nil }
func (k *heldKid) recv(_ *via.Ctx, v string) { k.Last.Set(v) }
func (k *heldKid) Inc(*via.Ctx)              { k.N.Set(k.N.Get() + 1) }
func (k *heldKid) View() h.H {
	return h.Div(h.Str("kid-n="), k.N.Display(), h.Str(" last="), k.Last.Display(), h.Button(on.Click(k.Inc)))
}

type heldHitsRoot struct {
	hits int
	Kid  heldKid
}

func (p *heldHitsRoot) Inc(*via.Ctx) { p.hits++ }
func (p *heldHitsRoot) View() h.H {
	return h.Div(h.P(h.Str("root-hits="+strconv.Itoa(p.hits))), h.Button(on.Click(p.Inc)), via.Child(&p.Kid))
}

func TestHeldRoot_actionsRunOnTheTabsInstance(t *testing.T) {
	t.Parallel()
	feed := topic.New[string]()
	app := vt.Serve(t, via.Handler(heldHitsRoot{Kid: heldKid{feed: feed}}))
	conn := app.Connect()
	defer conn.Close()
	inc := func(p *heldHitsRoot) func(*via.Ctx) { return p.Inc }

	for range 2 {
		status, _ := vt.Act(app, inc).Over(conn).Fire()
		require.Equal(t, http.StatusNoContent, status, "the stream carries the root's patch")
	}
	conn.Await("root-hits=2")
	feed.Publish("hi")
	conn.Await("last=hi")
	vt.Act(app, func(p *heldHitsRoot) func(*via.Ctx) { return p.Kid.Inc }).Over(conn).Fire()
	conn.Await("kid-n=1")
}

type heldFormRoot struct {
	Err string
	Kid heldKid
}

func (p *heldFormRoot) Save(ctx *via.Ctx) {
	if ctx.Request().FormValue("name") == "" {
		p.Err = "name-required"
		return
	}
	ctx.Redirect("/done")
}

func (p *heldFormRoot) View() h.H {
	return h.Div(h.P(h.Str("err=["+p.Err+"]")), via.PostForm(p.Save, h.Input(h.Name("name"))), via.Child(&p.Kid))
}

func TestHeldRoot_aNativeFormAnswersWithWhatItsHandlerSet(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(heldFormRoot{Kid: heldKid{feed: topic.New[string]()}}))
	conn := app.Connect()
	defer conn.Close()
	save := func(p *heldFormRoot) func(*via.Ctx) { return p.Save }

	status, body := vt.Act(app, save).Over(conn).Form(url.Values{}).Fire()
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "err=[name-required]", "a native submit replaces the page with the one its handler rendered")

	a := vt.Act(app, save).Over(conn).Form(url.Values{"name": {"ann"}})
	a.Fire()
	assert.Equal(t, "/done", a.Redirected())
}

type heldGateRoot struct {
	Open  bool
	Gated heldKid
}

func (p *heldGateRoot) Toggle(*via.Ctx) { p.Open = !p.Open }
func (p *heldGateRoot) View() h.H {
	return h.Div(h.P(h.Str("open="+strconv.FormatBool(p.Open))), h.Button(on.Click(p.Toggle)),
		via.When(p.Open, func() h.H { return via.Child(&p.Gated) }))
}

// heldKeepRoot streams from its first render, so the gated child opens on a live
// page.
type heldKeepRoot struct {
	heldGateRoot
	Keep heldKid
}

func (p *heldKeepRoot) View() h.H { return h.Div(p.heldGateRoot.View(), via.Child(&p.Keep)) }

func TestHeldRoot_aLiveChildABranchOpensConnects(t *testing.T) {
	t.Parallel()
	feed := topic.New[string]()
	app := vt.Serve(t, via.Handler(heldKeepRoot{heldGateRoot: heldGateRoot{Gated: heldKid{feed: feed}}, Keep: heldKid{feed: topic.New[string]()}}))
	conn := app.Connect()
	defer conn.Close()
	inc := func(p *heldKeepRoot) func(*via.Ctx) { return p.Gated.Inc }

	vt.Act(app, func(p *heldKeepRoot) func(*via.Ctx) { return p.Toggle }).Over(conn).Fire()
	conn.Await("open=true")
	for want := 1; want <= 2; want++ {
		status, _ := vt.Act(app, inc).Over(conn).Fire()
		require.Equal(t, http.StatusNoContent, status)
		conn.Await("kid-n=" + strconv.Itoa(want))
	}
	feed.Publish("hello")
	conn.Await("last=hello")
}

func TestHeldRoot_aLiveChildClosedAndReopenedKeepsItsState(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(heldGateRoot{Open: true, Gated: heldKid{feed: topic.New[string]()}}))
	conn := app.Connect()
	defer conn.Close()
	toggle := func(p *heldGateRoot) func(*via.Ctx) { return p.Toggle }
	inc := func(p *heldGateRoot) func(*via.Ctx) { return p.Gated.Inc }

	vt.Act(app, inc).Over(conn).Fire()
	conn.Await("kid-n=1")
	vt.Act(app, toggle).Over(conn).Fire()
	conn.Await("open=false")
	vt.Act(app, inc).Over(conn).Expect(http.StatusGone).Fire()
	vt.Act(app, toggle).Over(conn).Fire()
	assert.Contains(t, conn.Await("open=true"), "kid-n=1", "the reopened child renders the state it kept")
	vt.Act(app, inc).Over(conn).Fire()
	conn.Await("kid-n=2")
}

type heldResetRoot struct {
	Kid heldKid
}

func (p *heldResetRoot) Reset(*via.Ctx) { p.Kid.N.Set(42) }
func (p *heldResetRoot) View() h.H      { return h.Div(h.Button(on.Click(p.Reset)), via.Child(&p.Kid)) }

func TestHeldRoot_aRootActionShowsWhatItWroteToALiveChild(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(heldResetRoot{Kid: heldKid{feed: topic.New[string]()}}))
	conn := app.Connect()
	defer conn.Close()

	vt.Act(app, func(p *heldResetRoot) func(*via.Ctx) { return p.Reset }).Over(conn).Fire()
	conn.Await("kid-n=42")
	vt.Act(app, func(p *heldResetRoot) func(*via.Ctx) { return p.Kid.Inc }).Over(conn).Fire()
	conn.Await("kid-n=43")
}

type heldUser struct{ Name string }

type heldDash struct{ who string }

func (d *heldDash) OnInit(ctx *via.Ctx) error {
	d.who = "anon"
	if u, ok := ctx.Session().Get[heldUser](); ok {
		d.who = u.Name
	}
	return nil
}
func (d *heldDash) View() h.H { return h.P(h.Str("heldDash-who=" + d.who)) }

type heldLoginRoot struct {
	In   bool
	Kid  heldKid
	Dash heldDash
}

func (p *heldLoginRoot) Login(ctx *via.Ctx) {
	ctx.Session().Put(heldUser{Name: "ann"})
	ctx.Session().Rotate()
	p.In = true
}
func (p *heldLoginRoot) Noop(*via.Ctx) {}
func (p *heldLoginRoot) View() h.H {
	return h.Div(h.Button(on.Click(p.Login)), h.Button(on.Click(p.Noop)), via.Child(&p.Kid),
		via.When(p.In, func() h.H { return via.Child(&p.Dash) }))
}

func TestHeldRoot_aBranchALoginOpensReadsTheNewSession(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(heldLoginRoot{Kid: heldKid{feed: topic.New[string]()}}))
	conn := app.Connect()
	defer conn.Close()

	vt.Act(app, func(p *heldLoginRoot) func(*via.Ctx) { return p.Login }).Over(conn).Fire()
	conn.Await("heldDash-who=ann")
	status, _ := vt.Act(app, func(p *heldLoginRoot) func(*via.Ctx) { return p.Noop }).Over(conn).Fire()
	assert.Equal(t, http.StatusNoContent, status)
}
