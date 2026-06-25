//go:build browser

// Package vtbrowser_test exercises the vtbrowser harness in a real headless
// Chromium (run with -tags browser; VIA_CHROME overrides the binary path).
// Each test drives a harness method against a minimal via/v2 fixture, so the
// suite doubles as the browser tier: proving the harness works means proving
// Datastar's data-on:click / data-bind / SSE-morph behave under the strict
// nonce'd CSP — the bug class no httptest can see.
package vtbrowser_test

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via/v2"
	"github.com/go-via/via/v2/h"
	"github.com/go-via/via/v2/topic"
	"github.com/go-via/via/v2/vtbrowser"
)

// --- fixtures ---

// liveTicker pushes an incrementing count over SSE with no client interaction —
// the vehicle for testing that WaitFor observes a server-push morph.
type liveTicker struct{ n via.State[int] }

func (p *liveTicker) OnConnect(ctx *via.Ctx) error {
	ctx.Tick(80*time.Millisecond, p.tick)
	return nil
}
func (p *liveTicker) tick(ctx *via.Ctx) { p.n.Set(ctx, p.n.Get()+1) }
func (p *liveTicker) View() h.H         { return h.Div(h.P(h.Str("n: "), p.n.Display())) }

// clicker is a live island whose action mutates its own State — the vehicle for
// testing Click and the $_viatab → X-Via-Tab round-trip.
type clicker struct{ count via.State[int] }

func (c *clicker) Bump(ctx *via.Ctx)            { c.count.Set(ctx, c.count.Get()+1) }
func (c *clicker) OnConnect(ctx *via.Ctx) error { return nil }
func (c *clicker) View() h.H {
	return h.Div(h.P(h.Str("count: "), c.count.Display()), h.Button(via.OnClick(c.Bump), h.Str("+")))
}

// form is a stateless page with one bound input — the vehicle for Type and Value.
type form struct{ name via.Signal[string] }

func (f *form) View() h.H { return h.Div(h.Input(f.name.Bind(), h.RawAttr("placeholder", "name"))) }

// room + chat are a Topic-backed multi-user fixture (messages + presence) — the
// vehicle for NewTab fan-out, WaitValue (composer clear), and the no-clobber
// guarantee.
type room struct {
	bus      *topic.Topic[string]
	presence *topic.Topic[int]
	online   atomic.Int64
}

func newRoom() *room  { return &room{bus: topic.New[string](), presence: topic.New[int]()} }
func (r *room) join() { r.presence.Publish(int(r.online.Add(1))) }
func (r *room) part() { r.presence.Publish(int(r.online.Add(-1))) }

type chat struct {
	room   *room
	Draft  via.Signal[string]
	Log    via.List[string]
	Online via.State[int]
}

func (c *chat) OnConnect(ctx *via.Ctx) error {
	m := c.room.bus.Subscribe()
	ctx.OnDispose(m.Stop)
	via.Subscribe(ctx, m.C(), c.onMsg)
	p := c.room.presence.Subscribe()
	ctx.OnDispose(p.Stop)
	via.Subscribe(ctx, p.C(), c.onPres)
	c.room.join()
	ctx.OnDispose(c.room.part)
	return nil
}
func (c *chat) onMsg(ctx *via.Ctx, m string) { c.Log.Append(ctx, m) }
func (c *chat) onPres(ctx *via.Ctx, n int)   { c.Online.Set(ctx, n) }
func (c *chat) Send(ctx *via.Ctx) {
	if c.Draft.Get() == "" {
		return
	}
	c.room.bus.Publish(c.Draft.Get())
	c.Draft.Set(ctx, "")
}
func (c *chat) line(m string) h.H { return h.Li(h.Str(m)) }
func (c *chat) View() h.H {
	return h.Div(
		h.H1(h.Str("online: "), c.Online.Display()),
		h.Ul(via.Each(c.Log.Get(), c.line)),
		h.Form(via.OnSubmit(c.Send),
			h.Input(c.Draft.Bind(), h.RawAttr("placeholder", "msg")),
			h.Button(h.Str("send")),
		),
	)
}

// --- harness tests ---

// Open must serve the server-rendered skeleton (including the #root morph
// target) and Datastar must run under the strict nonce'd CSP without a single
// console error. Eval is the escape hatch for DOM facts the named helpers omit.
func TestOpen_servesSkeletonAndRunsDatastarCleanly(t *testing.T) {
	s := vtbrowser.Open(t, via.Register(clicker{}))

	if got := s.Text("p"); !strings.Contains(got, "count: 0") {
		t.Fatalf("Open did not serve the rendered skeleton: %q", got)
	}
	var hasRoot bool
	s.Eval(`!!document.getElementById('root')`, &hasRoot)
	if !hasRoot {
		t.Fatal("page is missing the #root morph target")
	}
	s.RequireCleanConsole()
}

// WaitFor polls the DOM, so it observes a value the server pushes over SSE with
// no client interaction — proving data-init opens the stream and each
// datastar-patch-elements frame morphs #root in a real browser.
func TestWaitFor_observesServerPushMorph(t *testing.T) {
	s := vtbrowser.Open(t, via.Register(liveTicker{}))

	s.WaitFor("p", func(text string) bool {
		var n int
		_, err := fmt.Sscanf(text, "n: %d", &n)
		return err == nil && n >= 2
	}, "the server-pushed count to reach 2 (live morph)")
	s.RequireCleanConsole()
}

// Click drives a live-island action: the count changes only if the $_viatab the
// SSE set is echoed as the X-Via-Tab header, reaching this connection's island
// and pushing the result back over its stream. WaitTextContains absorbs the
// round-trip latency.
func TestClick_roundTripsLiveActionThroughTabHeader(t *testing.T) {
	s := vtbrowser.Open(t, via.Register(clicker{}))

	s.WaitTextContains("p", "count: 0")
	s.Sleep(500 * time.Millisecond) // let the SSE connect so Datastar has $_viatab to echo
	s.Click("button")
	s.WaitTextContains("p", "count: 1")
	s.RequireCleanConsole()
}

// Type sends real key events (so Datastar's data-bind fires as for a human) and
// Value reads the bound input's resulting value.
func TestTypeAndValue_driveABoundInput(t *testing.T) {
	s := vtbrowser.Open(t, via.Register(form{}))

	s.Type("input", "alice")
	if got := s.Value("input"); got != "alice" {
		t.Fatalf("Type/Value round-trip failed: got %q, want %q", got, "alice")
	}
	s.RequireCleanConsole()
}

// NewTab opens a second tab in the same browser. A message sent in one tab must
// fan out (Topic → SSE → morph) to the other, presence must reflect both
// connections, and the sender's composer must clear — a deliberate signal-patch
// that WaitValue observes.
func TestNewTab_fansOutAndClearsComposerAcrossTabs(t *testing.T) {
	r := newRoom()
	a := vtbrowser.Open(t, via.Register(chat{room: r}))
	b := a.NewTab()

	a.WaitTextContains("h1", "online: 2") // both streams connected + presence settled

	a.Type("input", "hello")
	a.Sleep(250 * time.Millisecond) // let data-bind sync the typed signal
	a.Click("button")

	b.WaitTextContains("ul", "hello") // fan-out: B received A's message
	a.WaitValue("input", "")          // sender's composer cleared (signal-patch)

	a.RequireCleanConsole()
	b.RequireCleanConsole()
}

// A fan-out push must NOT clobber what another user is typing: while A composes
// (not yet sent), B sends; A must receive B's line AND keep its own draft. The
// element push omits data-signals precisely so a morph never overwrites a
// client signal a user is editing.
func TestNewTab_fanOutDoesNotClobberInProgressTyping(t *testing.T) {
	r := newRoom()
	a := vtbrowser.Open(t, via.Register(chat{room: r}))
	b := a.NewTab()

	a.WaitTextContains("h1", "online: 2")

	a.Type("input", "half-typed") // A is composing; has NOT sent
	a.Sleep(250 * time.Millisecond)

	b.Type("input", "from-b")
	b.Sleep(250 * time.Millisecond)
	b.Click("button") // B sends; fans out and pushes to A

	a.WaitTextContains("ul", "from-b") // A received B's fan-out
	if got := a.Value("input"); got != "half-typed" {
		t.Fatalf("fan-out clobbered A's in-progress draft: got %q, want %q", got, "half-typed")
	}

	a.RequireCleanConsole()
	b.RequireCleanConsole()
}
