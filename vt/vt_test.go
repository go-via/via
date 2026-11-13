package vt_test

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"testing"
	"testing/synctest"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// recorder stands in for the test's own T so a vt failure can be read
// instead of failing the test that provokes it.
type recorder struct {
	testing.TB
	mu    sync.Mutex
	fatal string
	logs  []string
}

func (r *recorder) Helper() {}

func (r *recorder) Fatalf(format string, args ...any) {
	r.mu.Lock()
	r.fatal = fmt.Sprintf(format, args...)
	r.mu.Unlock()
	runtime.Goexit()
}

func (r *recorder) Fatal(args ...any) { r.Fatalf("%s", fmt.Sprint(args...)) }

func (r *recorder) Log(args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.logs = append(r.logs, fmt.Sprint(args...))
}

func (r *recorder) failure(fn func()) string {
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn()
	}()
	<-done
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.fatal
}

type tally struct {
	N    via.State[int]
	Tail string
}

func (p *tally) Inc(ctx *via.Ctx) { p.N.Set(p.N.Get() + 1) }

// tallyInc names tally's one action for vt.Act.
func tallyInc(p *tally) func(*via.Ctx) { return p.Inc }

func (p *tally) View() h.H {
	return h.Div(h.Button(on.Click(p.Inc)), h.P(h.Str("n="), p.N.Display()), h.P(h.Str(p.Tail)))
}

func TestConnAwait_timeoutShowsTheFramesThatDidArrive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{TB: t}
		app := vt.Serve(rec, via.Handler(tally{}, via.WithLogger(vt.Logger(t))))
		conn := app.Connect()
		for range 3 {
			status, _ := vt.Act(app, tallyInc).Over(conn).Fire()
			require.Equal(t, http.StatusNoContent, status)
		}
		conn.Await("n=3")

		msg := rec.failure(func() { conn.Await("n=4") })
		assert.Contains(t, msg, `timed out after 2s waiting for "n=4"`)
		assert.Contains(t, msg, "4 frames on this stream, 0 read by this wait; the last 3:")
		assert.Contains(t, msg, "datastar-patch-elements: elements <div id=\"root\">")
		assert.Contains(t, msg, "n=3", "the newest frame must be among those shown")
		assert.NotContains(t, msg, "viatab", "only the last 3 frames are shown, not the first")
	})
}

func TestConnAwait_truncatesALongFrame(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{TB: t}
		app := vt.Serve(rec, via.Handler(tally{Tail: strings.Repeat("x", 5000) + "END"}, via.WithLogger(vt.Logger(t))))
		conn := app.Connect()
		status, _ := vt.Act(app, tallyInc).Over(conn).Fire()
		require.Equal(t, http.StatusNoContent, status)

		msg := rec.failure(func() { conn.Await("never") })
		assert.Contains(t, msg, "bytes cut")
		assert.Contains(t, msg, "END</p>", "the frame's tail must be shown")
		assert.Less(t, len(msg), 2000, "a 5 KB frame must not be printed whole")
	})
}

func TestConnAwait_closedStreamShowsTheFramesThatDidArrive(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{TB: t}
		r := via.Handler(tally{}, via.WithLogger(vt.Logger(t)))
		app := vt.Serve(rec, r)
		conn := app.Connect()
		r.Close()

		msg := rec.failure(func() { conn.Await("n=1") })
		assert.Contains(t, msg, `stream closed before "n=1" arrived`)
		assert.Contains(t, msg, "viatab", "the frames seen before the close must be listed")
	})
}

func TestLogger_sendsViaDiagnosticsToTheTestLog(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	via.Handler(tally{}, via.WithLogger(vt.Logger(rec)))
	rec.mu.Lock()
	defer rec.mu.Unlock()
	require.Len(t, rec.logs, 1)
	assert.Contains(t, rec.logs[0], "level=WARN")
	assert.Contains(t, rec.logs[0], "WithTrustedOrigin")
}

// finished runs its cleanups on demand, so a test can log into a T whose test
// has ended, where the real t.Log panics.
type finished struct {
	recorder
	cleanups []func()
}

func (f *finished) Cleanup(fn func()) { f.cleanups = append(f.cleanups, fn) }

func (f *finished) end() {
	for i := len(f.cleanups) - 1; i >= 0; i-- {
		f.cleanups[i]()
	}
}

func TestLogger_dropsARecordLoggedAfterTheTestEnded(t *testing.T) {
	t.Parallel()
	f := &finished{recorder: recorder{TB: t}}
	r := via.NewRouter(via.WithLogger(vt.Logger(f)))
	f.end()
	via.Mount(r, "/", misnamed{})
	f.mu.Lock()
	defer f.mu.Unlock()
	assert.Len(t, f.logs, 1, "only the startup warning, logged before the end")
}

type misnamed struct{}

func (*misnamed) View() h.H             { return h.Div() }
func (*misnamed) Oninit(*via.Ctx) error { return nil }

func TestConnAwait_findsAFrameLargerThanAScannerToken(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		app := vt.Serve(t, via.Handler(tally{Tail: strings.Repeat("x", 100<<10)}, via.WithLogger(vt.Logger(t))))
		conn := app.Connect()
		status, _ := vt.Act(app, tallyInc).Over(conn).Fire()
		require.Equal(t, http.StatusNoContent, status)
		conn.Await("n=1")
	})
}

func TestConnAwait_timeoutCountsTheFramesThisWaitRead(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		rec := &recorder{TB: t}
		app := vt.Serve(rec, via.Handler(tally{}, via.WithLogger(vt.Logger(t))))
		conn := app.Connect()
		status, _ := vt.Act(app, tallyInc).Over(conn).Fire()
		require.Equal(t, http.StatusNoContent, status)

		msg := rec.failure(func() { conn.Await("never") })
		assert.Contains(t, msg, "1 read by this wait")
	})
}

func TestAction_aNullBodyWithATabIsPostedVerbatim(t *testing.T) {
	t.Parallel()
	inner := via.Handler(tally{})
	var posted []byte
	app := vt.Serve(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posted, _ = io.ReadAll(r.Body)
			r.Body = io.NopCloser(bytes.NewReader(posted))
		}
		inner.ServeHTTP(w, r)
	}))
	vt.Act(app, tallyInc).Body("null").Tab("x").Expect(http.StatusGone).Fire()
	assert.Equal(t, "null", string(posted))
}

type actKid struct{ n int }

func (k *actKid) Bump(*via.Ctx) { k.n++ }
func (k *actKid) View() h.H {
	return h.Div(h.Button(on.Click(k.Bump)), h.P(h.Str(fmt.Sprintf("kid=%d", k.n))))
}

type actPage struct {
	Kid    actKid
	picked string
}

func (p *actPage) Pick(ctx *via.Ctx, s string) { p.picked = s }
func (p *actPage) Reset(*via.Ctx)              { p.picked = "reset" }
func (p *actPage) Unbound(*via.Ctx)            {}
func (p *actPage) View() h.H {
	return h.Div(
		h.Button(on.Click(on.Bind(p.Pick, "x"))), h.Button(on.Click(on.Bind(p.Pick, "y"))),
		via.Child(&p.Kid),
		h.Button(on.Click(p.Reset)), h.P(h.Str("picked="+p.picked)),
	)
}

func TestAct_postsTheNamedHandlerWhereverTheViewPutsIt(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(actPage{}))

	status, body := vt.Act(app, func(p *actPage) func(*via.Ctx) { return p.Reset }).Fire()
	require.Equal(t, http.StatusOK, status)
	assert.Contains(t, body, "picked=reset", "two row buttons and a child sit before Reset in the View")
}

func TestAct_findsAHandlerUnderTheChildThatRendersIt(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(actPage{}))

	act := vt.Act(app, func(p *actPage) func(*via.Ctx) { return p.Kid.Bump })
	assert.Contains(t, act.URL(), "/_via/a/Kid/")
	_, body := act.Fire()
	assert.Contains(t, body, "kid=1")
}

func TestActArg_postsTheRowCarryingTheArg(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(actPage{}))

	_, body := vt.ActArg(app, func(p *actPage) func(*via.Ctx, string) { return p.Pick }, "y").Fire()
	assert.Contains(t, body, "picked=y")
}

func TestAct_failsNamingAHandlerTheViewDoesNotBind(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	app := vt.Serve(rec, via.Handler(actPage{}))

	msg := rec.failure(func() { vt.Act(app, func(p *actPage) func(*via.Ctx) { return p.Unbound }).Fire() })
	assert.Contains(t, msg, "Unbound")
	assert.Contains(t, msg, "bound nowhere")
}

func TestKey_isTheChildsFieldPath(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "Kid", vt.Key(func(p *actPage) *actKid { return &p.Kid }))
}

func TestFire_failsARefusalTheTestDidNotExpect(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	app := vt.Serve(rec, via.Handler(actPage{}))

	msg := rec.failure(func() { app.Post("/_via/a/_/nope").Fire() })
	assert.Contains(t, msg, "answered 410")
	assert.Contains(t, msg, "chain .Expect(410)")
}

func TestFire_failsANamedHandlerByItsTypeAndMethod(t *testing.T) {
	t.Parallel()
	rec := &recorder{TB: t}
	app := vt.Serve(rec, via.Handler(actPage{}))

	msg := rec.failure(func() {
		vt.Act(app, func(p *actPage) func(*via.Ctx) { return p.Reset }).Expect(http.StatusGone).Fire()
	})
	assert.Contains(t, msg, "actPage.Reset answered 200, want one of [410]")
}

func TestExpect_letsFireReturnTheRefusal(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(actPage{}))

	status, body := app.Post("/_via/a/_/nope").Expect(http.StatusBadRequest, http.StatusGone).Fire()
	assert.Equal(t, http.StatusGone, status)
	assert.NotEmpty(t, body)
}

type sigKid struct {
	Say  via.Signal[string]
	said string
}

func (k *sigKid) Echo(*via.Ctx) { k.said = k.Say.Get() }
func (k *sigKid) View() h.H {
	return h.Div(h.Input(k.Say.Bind()), h.Button(on.Click(k.Echo)), h.P(h.Str("said="+k.said)))
}

type sigPage struct {
	Q    via.Signal[int]
	Form struct{ Name via.Signal[string] }
	Kid  sigKid
	got  string
}

func (p *sigPage) Read(*via.Ctx) { p.got = fmt.Sprintf("%d/%s", p.Q.Get(), p.Form.Name.Get()) }
func (p *sigPage) View() h.H {
	return h.Div(h.Input(p.Q.Bind()), h.Input(p.Form.Name.Bind()), h.Button(on.Click(p.Read)),
		h.P(h.Str("got="+p.got)), via.Child(&p.Kid))
}

func TestSet_sendsTheRootsAndANestedStructsSignals(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(sigPage{}))

	_, body := vt.Act(app, func(p *sigPage) func(*via.Ctx) { return p.Read }).
		Set(vt.Sig(func(p *sigPage) *via.Signal[int] { return &p.Q }, 7),
			vt.Sig(func(p *sigPage) *via.Signal[string] { return &p.Form.Name }, "ann")).Fire()
	assert.Contains(t, body, "got=7/ann")
}

func TestSet_sendsAChildsSignalUnderItsPrefix(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(sigPage{}))

	_, body := vt.Act(app, func(p *sigPage) func(*via.Ctx) { return p.Kid.Echo }).
		Set(vt.Sig(func(p *sigPage) *via.Signal[string] { return &p.Kid.Say }, "hi")).Fire()
	assert.Contains(t, body, "said=hi")
}

func TestSet_overlaysTheBody(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(sigPage{}))

	_, body := vt.Act(app, func(p *sigPage) func(*via.Ctx) { return p.Read }).
		Set(vt.Sig(func(p *sigPage) *via.Signal[int] { return &p.Q }, 7)).Body(`{"q":1,"form_name":"bo"}`).Fire()
	assert.Contains(t, body, "got=7/bo")
}

func TestSlot_isTheSignalsWireName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "form_name", vt.Slot(func(p *sigPage) *via.Signal[string] { return &p.Form.Name }))
	assert.Equal(t, "kid__say", vt.Slot(func(p *sigPage) *via.Signal[string] { return &p.Kid.Say }))
}

type who struct{ Name string }

type loginPage struct{ name string }

func (p *loginPage) OnInit(ctx *via.Ctx) error {
	u, _ := ctx.Session().Get[who]()
	p.name = u.Name
	return nil
}
func (p *loginPage) LogIn(ctx *via.Ctx) { ctx.Session().Put(who{Name: "ann"}) }
func (p *loginPage) View() h.H {
	return h.Div(h.Button(on.Click(p.LogIn)), h.P(h.Str("user="+p.name)))
}

func TestServe_keepsTheSessionCookieAcrossRequests(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(loginPage{}))

	vt.Act(app, func(p *loginPage) func(*via.Ctx) { return p.LogIn }).Fire()
	_, body := app.Get("/")
	assert.Contains(t, body, "user=ann")
}

type growList struct {
	rows *[]string
	got  string
}

func (p *growList) Add(*via.Ctx)              { *p.rows = append(*p.rows, fmt.Sprint(len(*p.rows))) }
func (p *growList) Pick(_ *via.Ctx, r string) { p.got = r }
func (p *growList) View() h.H {
	return h.Div(h.Button(on.Click(p.Add)),
		via.Each(*p.rows, func(r string) h.H { return h.Button(on.Click(on.Bind(p.Pick, r))) }),
		h.P(h.Str("got="+p.got)))
}

func TestActArg_findsARowAddedAfterThePageWasFirstRead(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(growList{rows: &[]string{}}))

	vt.Act(app, func(p *growList) func(*via.Ctx) { return p.Add }).Fire()
	_, body := vt.ActArg(app, func(p *growList) func(*via.Ctx, string) { return p.Pick }, "0").Fire()
	assert.Contains(t, body, "got=0")
}

func TestAt_actsOnAPageMountedAtAnotherPath(t *testing.T) {
	t.Parallel()
	r := via.NewRouter()
	via.Mount(r, "/", tally{})
	via.Mount(r, "/act", actPage{})
	app := vt.Serve(t, r)

	_, body := vt.Act(app, func(p *actPage) func(*via.Ctx) { return p.Reset }).At("/act").Fire()
	assert.Contains(t, body, "picked=reset")
}

type formPage struct{}

func (p *formPage) Submit(ctx *via.Ctx) { ctx.Redirect("/hi/" + ctx.Request().FormValue("name")) }
func (p *formPage) Leave(ctx *via.Ctx)  { ctx.Redirect("/bye") }
func (p *formPage) View() h.H {
	return h.Div(via.PostForm(p.Submit, h.Input(h.Name("name"))), h.Button(on.Click(p.Leave)))
}

func TestForm_submitsAPostFormAsTheBrowserDoes(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(formPage{}))

	act := vt.Act(app, func(p *formPage) func(*via.Ctx) { return p.Submit }).Form(url.Values{"name": {"ann"}})
	status, _ := act.Fire()
	assert.Equal(t, http.StatusSeeOther, status)
	assert.Equal(t, "/hi/ann", act.Redirected())
}

func TestRedirected_readsTheTargetOfAnActionsNavigation(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(formPage{}))

	act := vt.Act(app, func(p *formPage) func(*via.Ctx) { return p.Leave })
	act.Fire()
	assert.Equal(t, "/bye", act.Redirected())
}
