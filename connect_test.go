package via_test

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
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

// boundGateRoot binds a signal, so every push that carries the client's
// store renders twice (livePush): the branch-opened child must attach from
// the render that ran its OnInit.
type boundGateRoot struct {
	Name  via.Signal[string]
	Open  bool
	Keep  heldKid
	Gated heldKid
}

func (p *boundGateRoot) Toggle(*via.Ctx) { p.Open = !p.Open }
func (p *boundGateRoot) View() h.H {
	return h.Div(h.Input(p.Name.Bind()), h.P(h.Str("open="+strconv.FormatBool(p.Open))), h.Button(on.Click(p.Toggle)),
		via.Child(&p.Keep), via.When(p.Open, func() h.H { return via.Child(&p.Gated) }))
}

func TestHeldRoot_aBranchOpenedChildListensWhenThePushRendersTwice(t *testing.T) {
	t.Parallel()
	feed := topic.New[string]()
	app := vt.Serve(t, via.Handler(boundGateRoot{Keep: heldKid{feed: topic.New[string]()}, Gated: heldKid{feed: feed}}))
	conn := app.Connect()
	defer conn.Close()
	name := vt.Sig(func(p *boundGateRoot) *via.Signal[string] { return &p.Name }, "bob")

	vt.Act(app, func(p *boundGateRoot) func(*via.Ctx) { return p.Toggle }).Over(conn).Set(name).Fire()
	conn.Await("open=true")
	feed.Publish("hello")
	conn.Await("last=hello")
}

func TestHeldRoot_aRootWriteToALiveChildReachesItsContainer(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(heldResetRoot{Kid: heldKid{feed: topic.New[string]()}}))
	conn := app.Connect()
	defer conn.Close()

	vt.Act(app, func(p *heldResetRoot) func(*via.Ctx) { return p.Reset }).Over(conn).Fire()
	conn.Await("kid-n=42")
	// The browser keeps a live child's container through the root's morph
	// (data-ignore-morph), so only a frame of the child's own shows the write.
	conn.Await("selector #via-iKid")
	conn.Await("kid-n=42")
}

type secretKid struct {
	N   via.State[int]
	ran *atomic.Int32
}

func (k *secretKid) Do(*via.Ctx) { k.ran.Add(1) }
func (k *secretKid) View() h.H   { return h.Div(k.N.Display(), h.Button(on.Click(k.Do))) }

type signalGateRoot struct {
	Show   via.Signal[bool]
	Keep   heldKid
	Secret secretKid
}

func (p *signalGateRoot) Noop(*via.Ctx) {}
func (p *signalGateRoot) View() h.H {
	return h.Div(h.Input(p.Show.Bind()), h.Button(on.Click(p.Noop)), via.Child(&p.Keep),
		via.When(p.Show.Get(), func() h.H { return via.Child(&p.Secret) }))
}

func TestHeldRoot_aLiveChildOnlyAPostedSignalOpensTakesNoActions(t *testing.T) {
	t.Parallel()
	ran := new(atomic.Int32)
	app := vt.Serve(t, via.Handler(signalGateRoot{Keep: heldKid{feed: topic.New[string]()}, Secret: secretKid{ran: ran}}))
	conn := app.Connect()
	defer conn.Close()
	show := vt.Sig(func(p *signalGateRoot) *via.Signal[bool] { return &p.Show }, true)

	vt.Act(app, func(p *signalGateRoot) func(*via.Ctx) { return p.Noop }).Over(conn).Set(show).Fire()
	linesUntil(t, conn, `id="via-iSecret"`)
	vt.Act(app, func(p *signalGateRoot) func(*via.Ctx) { return p.Secret.Do }).Over(conn).Set(show).Expect(http.StatusGone).Fire()
	assert.Zero(t, ran.Load(), "the server's own render never opened it, so it is not dispatchable (I1)")
}

type mintKid struct{ N via.State[int] }

func (m *mintKid) OnInit(ctx *via.Ctx) error { ctx.Session().Put(heldUser{Name: "minted"}); return nil }
func (m *mintKid) View() h.H                 { return h.Div(h.Str("mint="), m.N.Display()) }

type mintGateRoot struct {
	Open bool
	Keep heldKid
	M    mintKid
}

func (p *mintGateRoot) Toggle(*via.Ctx) { p.Open = true }
func (p *mintGateRoot) View() h.H {
	return h.Div(h.Button(on.Click(p.Toggle)), via.Child(&p.Keep), via.When(p.Open, func() h.H { return via.Child(&p.M) }))
}

func TestHeldRoot_aBranchOpenedChildMintingASessionLeavesTheTabUsable(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(mintGateRoot{Keep: heldKid{feed: topic.New[string]()}}))
	conn := app.Connect()
	defer conn.Close()
	toggle := func(p *mintGateRoot) func(*via.Ctx) { return p.Toggle }

	vt.Act(app, toggle).Over(conn).Fire()
	conn.Await("mint=")
	status, _ := vt.Act(app, toggle).Over(conn).Fire()
	assert.Equal(t, http.StatusNoContent, status, "a session minted with no response to carry its cookie must not bind the tab")
}

type draftKid struct {
	N     via.State[int]
	Open  bool
	Draft via.Signal[string]
}

func (k *draftKid) OpenIt(*via.Ctx) { k.Open = true }
func (k *draftKid) View() h.H {
	return h.Div(k.N.Display(), h.Button(on.Click(k.OpenIt)), via.When(k.Open, func() h.H { return h.Input(k.Draft.Bind()) }))
}

type draftRoot struct {
	hits int
	Kid  draftKid
}

func (p *draftRoot) Bump(*via.Ctx) { p.hits++ }
func (p *draftRoot) View() h.H {
	return h.Div(h.P(h.Str("hits="+strconv.Itoa(p.hits))), h.Button(on.Click(p.Bump)), via.Child(&p.Kid))
}

func TestHeldRoot_aRootPushKeepsASlotTheChildOpened(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(draftRoot{}))
	conn := app.Connect()
	defer conn.Close()

	vt.Act(app, func(p *draftRoot) func(*via.Ctx) { return p.Kid.OpenIt }).Over(conn).Fire()
	conn.Await(`"kid__draft":""`)
	vt.Act(app, func(p *draftRoot) func(*via.Ctx) { return p.Bump }).Over(conn).Fire()
	assert.NotContains(t, linesUntil(t, conn, "hits=1"), `"kid__draft":`, "the root's push must not re-seed what the user typed")
}

// linesUntil reads conn's lines up to and including the first holding
// needle, so a test can assert what a push did not send before it.
func linesUntil(t *testing.T, conn *vt.Conn, needle string) string {
	t.Helper()
	var read strings.Builder
	for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); {
		line, ok := conn.Peek()
		if !ok {
			time.Sleep(5 * time.Millisecond)
			continue
		}
		read.WriteString(line + "\n")
		if strings.Contains(line, needle) {
			return read.String()
		}
	}
	require.Failf(t, "no line held the needle", "%q; read:\n%s", needle, read.String())
	return ""
}

type statsA struct{ hits int }

func (a *statsA) Hit(*via.Ctx) { a.hits++ }
func (a *statsA) View() h.H {
	return h.Div(h.Str("a-hits="+strconv.Itoa(a.hits)), h.Button(on.Click(a.Hit)))
}

type liveStats struct {
	N via.State[int]
	A statsA
}

func (s *liveStats) View() h.H { return h.Div(s.N.Display()) }

type statsRoot struct{ Stats liveStats }

func (p *statsRoot) View() h.H { return h.Div(via.Child(&p.Stats), via.Child(&p.Stats.A)) }

func TestHeldRoot_aPlainChildIsFoundInTheRenderThatHoldsIt(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(statsRoot{}))
	conn := app.Connect()
	defer conn.Close()

	status, _ := vt.Act(app, func(p *statsRoot) func(*via.Ctx) { return p.Stats.A.Hit }).Over(conn).Fire()
	assert.Equal(t, http.StatusNoContent, status, "the root rendered Stats-A, not the live Stats its key extends")
	conn.Await("a-hits=1")
}

type liveBranchRoot struct {
	Open bool
	N    via.State[int]
	Keep heldKid
}

func (p *liveBranchRoot) Toggle(*via.Ctx) { p.Open = true }
func (p *liveBranchRoot) View() h.H {
	return h.Div(h.Button(on.Click(p.Toggle)), via.Child(&p.Keep), via.When(p.Open, func() h.H { return p.N.Display() }))
}

func TestHeldRoot_aPlainRootTurningLiveEndsTheStream(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(liveBranchRoot{Keep: heldKid{feed: topic.New[string]()}}, via.WithLogger(vt.Logger(t))))
	conn := app.Connect()
	defer conn.Close()

	vt.Act(app, func(p *liveBranchRoot) func(*via.Ctx) { return p.Toggle }).Over(conn).Fire()
	require.NoError(t, conn.AwaitClose(), "the client reloads and gets a page that tells the truth")
}

// greetKid sets a signal and publishes its arrival when it connects.
type greetKid struct {
	S     via.Signal[int]
	N     via.State[int]
	joins *topic.Topic[string]
}

func (k *greetKid) OnInit(ctx *via.Ctx) error { ctx.OnConnect(k.greet); return nil }
func (k *greetKid) greet() {
	k.S.Set(7)
	if k.joins != nil {
		k.joins.Publish("joined")
	}
}
func (k *greetKid) View() h.H { return h.Div(k.S.Display(), k.N.Display()) }

type pairGateRoot struct {
	Open  bool
	Keep  heldKid
	Greet greetKid
	Hear  heldKid
}

func (p *pairGateRoot) Toggle(*via.Ctx) { p.Open = true }
func (p *pairGateRoot) View() h.H {
	return h.Div(h.Button(on.Click(p.Toggle)), via.Child(&p.Keep),
		via.When(p.Open, func() h.H { return h.Div(via.Child(&p.Greet), via.Child(&p.Hear)) }))
}

func TestHeldRoot_childrenOneActionOpensAllListenBeforeAnyConnects(t *testing.T) {
	t.Parallel()
	joins := topic.New[string]()
	app := vt.Serve(t, via.Handler(pairGateRoot{Keep: heldKid{feed: topic.New[string]()},
		Greet: greetKid{joins: joins}, Hear: heldKid{feed: joins}}))
	conn := app.Connect()
	defer conn.Close()

	vt.Act(app, func(p *pairGateRoot) func(*via.Ctx) { return p.Toggle }).Over(conn).Fire()
	conn.Await(vt.Slot(func(p *pairGateRoot) *via.Signal[int] { return &p.Greet.S }) + `":7`)
	conn.Await("last=joined")
}

type whoKid struct {
	feed *topic.Topic[string]
	Who  via.State[string]
}

func (k *whoKid) OnInit(ctx *via.Ctx) error { ctx.Listen(k.feed, k.recv); return nil }
func (k *whoKid) recv(ctx *via.Ctx, _ string) {
	u, _ := ctx.Session().Get[heldUser]()
	k.Who.Set(u.Name)
}
func (k *whoKid) View() h.H { return h.Div(h.Str("who="), k.Who.Display()) }

type mintLoginRoot struct {
	Open bool
	Who  whoKid
	M    mintKid
}

func (p *mintLoginRoot) Toggle(*via.Ctx) { p.Open = true }
func (p *mintLoginRoot) Login(ctx *via.Ctx) {
	ctx.Session().Put(heldUser{Name: "ann"})
	ctx.Session().Rotate()
}
func (p *mintLoginRoot) View() h.H {
	return h.Div(h.Button(on.Click(p.Toggle)), h.Button(on.Click(p.Login)), via.Child(&p.Who),
		via.When(p.Open, func() h.H { return via.Child(&p.M) }))
}

func TestHeldRoot_aLoginAfterAMidStreamMintReachesTheHandlers(t *testing.T) {
	t.Parallel()
	feed := topic.New[string]()
	app := vt.Serve(t, via.Handler(mintLoginRoot{Who: whoKid{feed: feed}}))
	conn := app.Connect()
	defer conn.Close()

	vt.Act(app, func(p *mintLoginRoot) func(*via.Ctx) { return p.Toggle }).Over(conn).Fire()
	conn.Await("mint=")
	vt.Act(app, func(p *mintLoginRoot) func(*via.Ctx) { return p.Login }).Over(conn).Fire()
	feed.Publish("tick")
	conn.Await("who=ann")
}

type formA struct{ Err string }

func (a *formA) Save(ctx *via.Ctx) {
	if ctx.Request().FormValue("name") == "" {
		a.Err = "a-name-required"
	}
}
func (a *formA) View() h.H {
	return h.Div(h.Str("aerr=["+a.Err+"]"), via.PostForm(a.Save, h.Input(h.Name("name"))))
}

type formStats struct {
	N via.State[int]
	A formA
}

func (s *formStats) View() h.H { return h.Div(s.N.Display()) }

type formStatsRoot struct{ Stats formStats }

func (p *formStatsRoot) View() h.H { return h.Div(via.Child(&p.Stats), via.Child(&p.Stats.A)) }

func TestHeldRoot_aNativeFormInAPlainChildBesideALiveOneStaysPlain(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(formStatsRoot{}))
	conn := app.Connect()
	defer conn.Close()

	status, body := vt.Act(app, func(p *formStatsRoot) func(*via.Ctx) { return p.Stats.A.Save }).Over(conn).Form(url.Values{}).Fire()
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "aerr=[a-name-required]")
}

type growKid struct {
	N    via.State[int]
	Open bool
	Note heldDash
}

func (k *growKid) Grow(*via.Ctx) { k.Open = true }
func (k *growKid) View() h.H {
	return h.Div(k.N.Display(), h.Button(on.Click(k.Grow)), via.When(k.Open, func() h.H { return via.Child(&k.Note) }))
}

type growRoot struct {
	hits int
	Kid  growKid
}

func (p *growRoot) Bump(*via.Ctx) { p.hits++ }
func (p *growRoot) View() h.H {
	return h.Div(h.P(h.Str("hits="+strconv.Itoa(p.hits))), h.Button(on.Click(p.Bump)), via.Child(&p.Kid))
}

func TestHeldRoot_aRootPushCarriesAPlainChildALiveChildOpened(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(growRoot{}))
	conn := app.Connect()
	defer conn.Close()

	vt.Act(app, func(p *growRoot) func(*via.Ctx) { return p.Kid.Grow }).Over(conn).Fire()
	conn.Await("heldDash-who=")
	vt.Act(app, func(p *growRoot) func(*via.Ctx) { return p.Bump }).Over(conn).Fire()
	conn.Await("hits=1")
}
