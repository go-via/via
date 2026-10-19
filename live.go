package via

import (
	"context"
	"fmt"
	"runtime/debug"
	"time"

	"github.com/go-via/via/topic"
)

type tickReg struct {
	d  time.Duration
	fn func(*Ctx)
}

// subscription is one ctx.Listen, erased of its topic's element type. OnInit
// records it; only the SSE handler subscribes it.
type subscription interface {
	// start subscribes onto the connection's shared wake channel. It spawns no
	// goroutine.
	start(wake chan struct{})
	// drain takes the whole queued backlog and reports whether it was non-empty.
	drain() bool
	// deliver runs the handler over the drained backlog, then pushes.
	deliver()
}

type listenSub[T any] struct {
	c       *Ctx
	t       *topic.Topic[T]
	handler func(*Ctx, T)
	sub     *topic.Sub[T]
	batch   []T
}

func (l *listenSub[T]) start(wake chan struct{}) {
	// This call and runStream's disposer sweep both run on the one stream
	// goroutine, the start strictly first, so the append needs no lock.
	l.sub = l.t.Subscribe()
	l.sub.WakeOn(wake)
	// Not c.OnDispose: this runs at stream start, long after OnInit returned,
	// and the public method rejects (and logs) a late call.
	l.c.disposers = append(l.c.disposers, l.sub.Stop)
}

// drain takes one batch per push item: values published while the item runs
// pile up for the next drain, so a burst costs a bounded number of frames.
func (l *listenSub[T]) drain() bool {
	l.batch, _ = l.sub.Drain()
	return len(l.batch) > 0
}

// deliver pushes only this unit's container, so a fan-out never re-renders a
// sibling. One recover per value, and the push runs regardless: the batch is a
// frame-rate optimisation, not a failure domain, so a panic on value k must not
// swallow k+1..n or the re-render the surviving values earned.
func (l *listenSub[T]) deliver() {
	for _, v := range l.batch {
		l.callHandler(v)
	}
	l.batch = nil
	l.c.pusher.push()
}

// callHandler recovers per call, so a panicking handler does not tear down
// the connection's push goroutine.
func (l *listenSub[T]) callHandler(v T) {
	defer l.c.recoverListener()
	l.handler(l.c, v)
}

func (c *Ctx) recoverListener() {
	r := recover()
	if r == nil {
		return
	}
	if c.guard != nil {
		c.guard.panicked(c.logger(), r)
		return
	}
	c.logger().Error("via: panic in a Listen handler",
		"unit", fmt.Sprintf("%T", c.unitV.v), "err", r, "stack", string(debug.Stack()))
}

// Tick schedules fn to run every d for the life of the unit's connection, and
// is one of three things that make a unit live (Listen and rendering a State or
// List are the others). After each run via re-renders the unit and pushes an
// element-patch. A d of 0 or less runs every second instead, and the Router
// logs one warning. Valid only inside OnInit: ticks and subs are snapshotted
// there, so a later call registers nothing and logs loudly.
func (c *Ctx) Tick(d time.Duration, fn func(*Ctx)) {
	if !c.initOnly("Tick") {
		return
	}
	if d <= 0 {
		d = time.Second
		c.warnTickClamp()
	}
	c.live = true
	c.ticks = append(c.ticks, tickReg{d: d, fn: fn})
}

// warnTickClamp logs once per Router: OnInit runs on every GET and connect, so
// a per-call warning would repeat for every visitor.
func (c *Ctx) warnTickClamp() {
	msg := "via: Tick interval <= 0, running every 1s instead"
	if c.policy == nil {
		c.logger().Warn(msg)
		return
	}
	if c.policy.tickWarned.CompareAndSwap(false, true) {
		c.policy.log.Warn(msg)
	}
}

// OnConnect registers fn to run once when this unit's stream opens — the
// acquire half of OnDispose (join a room, claim a slot). OnInit itself runs on
// every request that renders the unit, so an acquire there would fire on
// requests that never become a connection. Valid only inside OnInit, and it
// does not itself make a unit live: on a unit nothing else made live, fn never
// runs. Like Tick, a call after OnInit returned registers nothing and logs.
//
// A Set inside fn reaches the client: one push follows the connect. fn runs
// after every Listen subscribes, so it's where to re-read a store a Listen
// mirrors — a publish before the subscribe else reaches no handler.
func (c *Ctx) OnConnect(fn func()) {
	if !c.initOnly("OnConnect") {
		return
	}
	c.onConnect = append(c.onConnect, fn)
}

// OnDispose registers a teardown function run when the unit's connection
// closes — stop subscriptions, release producers. fn is a named method value
// (e.g. sub.Stop). Valid only inside OnInit, and only meaningful on a unit
// something else has already made live: a unit that never ticks, listens, or
// renders State opens no connection to tear down. Like Tick, a call from
// OnReload or after OnInit returned registers nothing and logs.
func (c *Ctx) OnDispose(fn func()) {
	if !c.initOnly("OnDispose") {
		return
	}
	c.disposers = append(c.disposers, fn)
}

// Listen wires a unit to a Topic: it subscribes, pumps every published value
// into handler on its tab's stream goroutine (serialized with Tick, so unit state
// is mutated race-free), pushes this unit's re-render, and stops on disconnect.
// Like Tick it makes the unit live and is valid only inside OnInit.
//
// Every published value reaches handler exactly once, in publish order, up to
// the subscription's queue limit (see topic.Publish for the one drop case,
// which logs; Listen owns the Sub, so hold your own Subscribe to read
// Sub.Dropped). Renders are not one per value — a whole backlog runs its
// handler calls, then one re-render and one SSE frame — so a burst costs frames
// proportional to how fast the client drains, not how fast the topic publishes.
//
// The Subscribe is deferred to the SSE handler: OnInit also runs on a plain GET
// and every plain action, and subscribing there would hand out a Sub nothing
// will ever Stop. It happens before any OnConnect fn runs, so a unit that
// publishes on connect observes its own publish.
//
// handler's ctx.Session() is the session the stream connected with, and a
// write to it from any request reaches handler: a logout in another tab is
// seen at once in this process, and on the next keepalive when it happened on
// another process (see [Session]). A tab that connected before its session
// existed sees none until an action on one of its live units carries a
// session cookie: that binds the tab to the session, and handler sees it from
// then on (a reconnect with the cookie does the same). An action on a plain
// child of the page does not bind it, and neither does a session minted by
// another tab. Read the id from ctx.Session() in handler rather than caching it in
// OnInit, and mint the session in OnInit if every tab must be addressable by
// session from its first frame.
func (c *Ctx) Listen[T any](t *topic.Topic[T], handler func(*Ctx, T)) {
	if !c.initOnly("Listen") {
		return
	}
	c.live = true
	c.subs = append(c.subs, &listenSub[T]{c: c, t: t, handler: handler})
}

// runTicker queues t on pushq every t.d until reqCtx ends. The item pushes
// only this unit's container, so a tick never re-renders a sibling.
func runTicker(reqCtx context.Context, unit *Ctx, t tickReg, pushq chan<- pushItem) {
	tk := time.NewTicker(t.d)
	defer tk.Stop()
	for {
		select {
		case <-reqCtx.Done():
			return
		case <-tk.C:
			select {
			case pushq <- pushItem{unit: unit, tick: t.fn}:
			case <-reqCtx.Done():
				return
			}
		}
	}
}

// initOnly reports whether a registration named name may run: only inside
// OnInit, and never from OnReload, since liveness is the GET/connect verdict
// and a reload may not raise it. A refused call is logged.
func (c *Ctx) initOnly(name string) bool {
	switch {
	case c.inReload:
		c.logger().Warn("via: " + name + " called from OnReload — ignored; " + name + " is valid only inside OnInit")
		return false
	case !c.inInit:
		c.logger().Warn("via: " + name + " called after OnInit returned — ignored; " + name + " is valid only inside OnInit")
		return false
	}
	return true
}
