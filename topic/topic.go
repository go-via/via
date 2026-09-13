// Package topic is an in-process fan-out broker for live embeds: one Publish
// reaches every Subscriber. It is the blessed multi-user seam that keeps the
// via core free of shared state — apps create a Topic, pipe it into an embed
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
	mu   sync.Mutex
	subs map[*Sub[T]]struct{}
}

// New creates an empty Topic.
func New[T any]() *Topic[T] {
	return &Topic[T]{subs: make(map[*Sub[T]]struct{})}
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

// Subs returns the number of live subscriptions — the head-count an app
// publishes as presence, and the number that must fall back to zero once every
// tab has disconnected.
func (t *Topic[T]) Subs() int {
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
	for s := range t.subs {
		s.enqueue(v)
	}
}

func (s *Sub[T]) enqueue(v T) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return
	}
	if len(s.q) >= s.limit {
		s.dropped++
		warn := !s.warned
		s.warned = true
		s.mu.Unlock()
		if warn {
			log.Printf("via/topic: subscriber backlog hit its %d-value limit; dropping newest values for this subscriber only", s.limit)
		}
		return
	}
	s.q = append(s.q, v)
	s.mu.Unlock()
	select {
	case s.ready <- struct{}{}:
	default: // already signalled; Drain takes the whole backlog
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
	s.mu.Unlock()
	close(s.ready)
}
