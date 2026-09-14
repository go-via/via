package via

import (
	"context"
	"log"
	"runtime/debug"
	"time"

	"github.com/go-via/via/topic"
)

type tickReg struct {
	d  time.Duration
	fn func(*Ctx)
}

// subStarter subscribes one ctx.Listen onto the connection's shared wake
// channel and returns the listener runStream polls. Spawns no goroutine.
type subStarter func(wake chan struct{}) listener

// listener is one live subscription, erased of its topic's element type. poll
// takes the whole queued backlog and returns its work item, or nil.
type listener struct{ poll func() func() }

// Tick schedules fn to run every d for the life of the unit's connection, and
// is one of the two things that make a unit LIVE (rendering a State or List is
// the other). After each run via re-renders the unit and pushes an
// element-patch. Valid only inside OnInit: ticks and subs are snapshotted
// there, so a later call registers nothing and logs loudly.
func (c *Ctx) Tick(d time.Duration, fn func(*Ctx)) {
	if c.reinit {
		return // the post-action re-run; this unit's ticks were snapshotted at GET/connect (I5)
	}
	if c.initDone {
		log.Print("via: Tick called after OnInit returned — ignored; Tick is valid only inside OnInit")
		return
	}
	c.live = true
	c.ticks = append(c.ticks, tickReg{d: d, fn: fn})
}

// OnConnect registers fn to run once when this unit's stream opens — the
// acquire half of OnDispose (join a room, claim a slot). OnInit itself runs on
// every request that renders the unit, so an acquire there would fire on
// requests that never become a connection. Valid only inside OnInit, and it
// does not itself make a unit live: on a unit nothing else made live, fn never
// runs. Like Tick, a call after OnInit returned registers nothing and logs.
func (c *Ctx) OnConnect(fn func()) {
	if c.reinit {
		return // see Tick
	}
	if c.initDone {
		log.Print("via: OnConnect called after OnInit returned — ignored; OnConnect is valid only inside OnInit")
		return
	}
	c.onConnect = append(c.onConnect, fn)
}

// OnDispose registers a teardown function run when the unit's connection
// closes — stop subscriptions, release producers. fn is a named method value
// (e.g. sub.Stop). Valid only inside OnInit, and only meaningful on a unit
// something else has already made live: a unit that never ticks, listens, or
// renders State opens no connection to tear down. Like Tick, a call after
// OnInit returned registers nothing and logs.
func (c *Ctx) OnDispose(fn func()) {
	if c.reinit {
		return // see Tick
	}
	if c.initDone {
		log.Print("via: OnDispose called after OnInit returned — ignored; OnDispose is valid only inside OnInit")
		return
	}
	c.disposers = append(c.disposers, fn)
}

// Listen wires a unit to a Topic: it subscribes, pumps every published value
// into handler on the unit's own goroutine (serialized with Tick, so unit state
// is mutated race-free), pushes this unit's re-render, and stops on disconnect.
// Like Tick it makes the unit live and is valid only inside OnInit.
//
// Every published value reaches handler exactly once, in publish order, up to
// the subscription's queue limit (see topic.Publish for the one drop case,
// which logs; Listen owns the Sub, so hold your own Subscribe to read
// Sub.Dropped). Renders are NOT one per value — a whole backlog runs its
// handler calls, then one re-render and one SSE frame — so a burst costs frames
// proportional to how fast the client drains, not how fast the topic publishes.
//
// The Subscribe is deferred to the SSE handler: OnInit also runs on a plain GET
// and every plain action, and subscribing there would hand out a Sub nothing
// will ever Stop. It happens before any OnConnect fn runs, so a unit that
// publishes on connect observes its own publish.
func (c *Ctx) Listen[T any](t *topic.Topic[T], handler func(*Ctx, T)) {
	if c.reinit {
		return // see Tick
	}
	if c.initDone {
		log.Print("via: Listen called after OnInit returned — ignored; Listen is valid only inside OnInit")
		return
	}
	c.live = true
	c.subs = append(c.subs, func(wake chan struct{}) listener {
		// This call and runStream's disposer sweep both run on the one stream
		// goroutine, the starter strictly first, so the append needs no lock.
		sub := t.Subscribe()
		sub.WakeOn(wake)
		// Not c.OnDispose: this runs at stream start, long after OnInit
		// returned, and the public method rejects (and logs) a late call.
		c.disposers = append(c.disposers, sub.Stop)
		return listener{poll: func() func() {
			batch, _ := sub.Drain()
			if len(batch) == 0 {
				return nil
			}
			// One push item per BATCH: values published while this item runs
			// pile up for the next Drain, so a burst costs a bounded number of
			// frames. The item pushes only THIS embed's container, so a fan-out
			// never re-renders a sibling.
			return func() {
				// One recover per VALUE, and the push runs regardless: the
				// batch is a frame-rate optimisation, not a failure domain, so
				// a panic on value k must not swallow k+1..n or the re-render
				// the surviving values earned.
				for _, v := range batch {
					callListener(c, handler, v)
				}
				c.push()
			}
		}}
	})
}

// callListener recovers per call, so a panicking handler does not tear down
// the connection's push goroutine.
func callListener[T any](c *Ctx, handler func(*Ctx, T), v T) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("via: panic in a Listen handler [unit=%T]: %v\n%s", c.embedV.v, r, debug.Stack())
		}
	}()
	handler(c, v)
}

func startTicker(reqCtx context.Context, embed *Ctx, t tickReg, pushq chan<- func()) {
	go func() {
		tk := time.NewTicker(t.d)
		defer tk.Stop()
		for {
			select {
			case <-reqCtx.Done():
				return
			case <-tk.C:
				// Push only this embed's container, so a tick never re-renders
				// a sibling.
				select {
				case pushq <- func() { t.fn(embed); embed.push() }:
				case <-reqCtx.Done():
					return
				}
			}
		}
	}()
}
