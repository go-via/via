// Package topic is an in-process fan-out broker for live children: one Publish
// reaches every Subscriber. It is the blessed multi-user seam that keeps the
// via core free of shared state — apps create a Topic, pipe it into a child
// via ctx.Listen, and Publish to it from an action or any goroutine.
// Durability, replay, and cross-pod delivery are explicitly out of scope: put a
// real bus or database behind a Topic, never inside it.
package topic

import (
	"log"
	"sync"
)

// Topic is a typed fan-out broker. Its zero value is not usable; call New.
type Topic[T any] struct {
	mu    sync.Mutex
	subs  map[*Sub[T]]struct{}
	wakes map[chan<- struct{}]struct{} // scratch, reused by Publish
}

// New builds a Topic with no subscribers. A Topic is safe for concurrent use
// and is meant to live for the process: declare it as a package-level var and
// let children subscribe to it with ctx.Listen. It has no Close; a Topic with no
// subscribers costs a map lookup per publish.
func New[T any]() *Topic[T] {
	return &Topic[T]{
		subs:  make(map[*Sub[T]]struct{}),
		wakes: make(map[chan<- struct{}]struct{}),
	}
}

// DefaultLimit bounds how many undelivered values one subscriber may hold. It
// is a runaway guard, not a rate limit: a consumer drains its whole backlog in
// one batch, so reaching this means the consumer is wedged, not merely behind.
const DefaultLimit = 100_000

// Sub is a subscription. Wait on Ready, take the backlog with Drain, call Stop
// to unsubscribe.
type Sub[T any] struct {
	topic *Topic[T]
	ready chan struct{} // capacity 1, coalescing: "there is work"

	mu      sync.Mutex
	wake    chan<- struct{} // optional extra notifier, see WakeOn
	q       []T
	limit   int
	dropped int
	warned  bool
	stopped bool
}

// Subscribe registers a new subscriber with DefaultLimit queue capacity.
func (t *Topic[T]) Subscribe() *Sub[T] { return t.SubscribeLimit(DefaultLimit) }

// SubscribeLimit registers a new subscriber holding at most limit undelivered
// values. A limit below 1 means DefaultLimit.
func (t *Topic[T]) SubscribeLimit(limit int) *Sub[T] {
	if limit < 1 {
		limit = DefaultLimit
	}
	s := &Sub[T]{topic: t, ready: make(chan struct{}, 1), limit: limit}
	t.mu.Lock()
	t.subs[s] = struct{}{}
	t.mu.Unlock()
	return s
}

// NumSubs returns the number of live subscriptions — the head-count an app
// publishes as presence, and the number that must fall back to zero once every
// tab has disconnected.
func (t *Topic[T]) NumSubs() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.subs)
}

// Publish queues v for every current subscriber and returns without blocking:
// no subscriber can stall the publisher or starve the others, and a unit may
// safely Publish to a topic it also listens to. Delivery is lossless up to each
// subscriber's queue limit; past it Publish drops v for that subscriber only,
// counts it in Sub.Dropped, and logs once per subscription.
func (t *Topic[T]) Publish(v T) {
	t.mu.Lock()
	defer t.mu.Unlock()
	// One fan-out releases ONE wake-up per distinct channel, and only once
	// every subscriber holds v: a token released mid-fan-out guarantees its
	// sweep straddles this publish, and a second token for the same fan-out
	// buys a spurious extra sweep that overlaps the NEXT one. Hence: collect
	// the distinct wake channels, then signal each exactly once.
	//
	// This does NOT make a fan-out atomic for a reader. A sweep already in
	// flight holds no lock here and polls its subscriptions in registration
	// order, so it can still drain one before v landed and another after.
	// Nothing is lost — this publish's token is still pending, so the
	// straddled subscription is drained on the next sweep. It costs an extra
	// frame, not a split delivery.
	for s := range t.subs {
		if wake, ok := s.enqueue(v); ok && wake != nil {
			t.wakes[wake] = struct{}{}
		}
	}
	for w := range t.wakes {
		signal(w)
	}
	clear(t.wakes)
}

// enqueue queues v and returns this subscription's wake channel, which the
// caller must signal once the whole fan-out is queued. ok is false when v was
// not queued.
func (s *Sub[T]) enqueue(v T) (wake chan<- struct{}, ok bool) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return nil, false
	}
	if len(s.q) >= s.limit {
		s.dropped++
		warn := !s.warned
		s.warned = true
		s.mu.Unlock()
		if warn {
			log.Printf("via/topic: subscriber backlog hit its %d-value limit; dropping newest values for this subscriber only", s.limit)
		}
		return nil, false
	}
	s.q = append(s.q, v)
	wake = s.wake
	s.mu.Unlock()
	select {
	case s.ready <- struct{}{}:
	default: // already signalled; Drain takes the whole backlog
	}
	return wake, true
}

// signal pokes a coalescing capacity-1 notifier without ever blocking the
// publisher.
func signal(ch chan<- struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// WakeOn routes this subscription's wake-ups to ch as well as to Ready, so ONE
// reader can multiplex many subscriptions on a single channel — a live
// connection drives every ctx.Listen from its own select loop instead of
// spending a goroutine per subscription. ch must be buffered (capacity 1 is
// enough: wake-ups coalesce and carry no values), and the reader must Drain
// EVERY subscription it multiplexes on each wake-up, since one token may stand
// for any number of them.
//
// It signals ch immediately when the queue is already non-empty, so a value
// published between Subscribe and WakeOn is not stranded, and Stop signals it
// too, so a reader parked on ch alone still sees the end.
func (s *Sub[T]) WakeOn(ch chan<- struct{}) {
	s.mu.Lock()
	s.wake = ch
	pending := len(s.q) > 0 || s.stopped
	s.mu.Unlock()
	if pending {
		signal(ch)
	}
}

// Ready fires whenever the queue goes from empty to non-empty, and is closed by
// Stop. It carries no values: one wake-up may cover any number of them, so
// every wake-up must be followed by Drain.
func (s *Sub[T]) Ready() <-chan struct{} { return s.ready }

// Drain removes and returns every queued value in publish order. ok is false
// once the subscription is stopped AND its backlog is exhausted, which is the
// reader's signal to exit. A nil slice with ok true just means a spurious
// wake-up.
func (s *Sub[T]) Drain() (batch []T, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	batch, s.q = s.q, nil
	return batch, len(batch) > 0 || !s.stopped
}

// Dropped reports how many values this subscription lost to its queue limit.
// It is zero unless the reader wedged.
func (s *Sub[T]) Dropped() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// Stop unsubscribes and closes Ready, ending the reader's loop. It is
// idempotent. Values already queued stay drainable, so a reader that wakes on
// the close still sees everything published before it.
func (s *Sub[T]) Stop() {
	s.topic.mu.Lock()
	_, live := s.topic.subs[s]
	delete(s.topic.subs, s)
	s.topic.mu.Unlock()
	if !live {
		return
	}
	s.mu.Lock()
	s.stopped = true
	wake := s.wake
	s.mu.Unlock()
	close(s.ready)
	signal(wake)
}
