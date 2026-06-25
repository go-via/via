// Package topic is an in-process, fire-and-forget fan-out broker for live
// islands: one Publish reaches every Subscriber. It is the blessed multi-user
// seam that keeps the via core free of shared state — apps create a Topic, pipe
// it into an island via via.Subscribe, and Publish to it from an action or any
// goroutine. Durability, replay, and cross-pod delivery are explicitly out of
// scope: put a real bus or database behind a Topic, never inside it.
package topic

import "sync"

// Topic is a typed fan-out broker. Its zero value is not usable; call New.
type Topic[T any] struct {
	mu   sync.Mutex
	subs map[*Sub[T]]struct{}
}

// New creates an empty Topic.
func New[T any]() *Topic[T] {
	return &Topic[T]{subs: make(map[*Sub[T]]struct{})}
}

// Sub is a subscription. Read values from C(); call Stop to unsubscribe.
type Sub[T any] struct {
	ch    chan T
	topic *Topic[T]
}

// subBuffer bounds each subscriber's queue. A consumer that falls this far
// behind drops the newest values rather than stalling the publisher.
const subBuffer = 64

// Subscribe registers a new subscriber and returns it.
func (t *Topic[T]) Subscribe() *Sub[T] {
	s := &Sub[T]{ch: make(chan T, subBuffer), topic: t}
	t.mu.Lock()
	t.subs[s] = struct{}{}
	t.mu.Unlock()
	return s
}

// Publish delivers v to every current subscriber. It never blocks: a subscriber
// whose buffer is full drops v, so one slow consumer cannot stall the publisher
// or starve the others.
func (t *Topic[T]) Publish(v T) {
	t.mu.Lock()
	defer t.mu.Unlock()
	for s := range t.subs {
		select {
		case s.ch <- v:
		default: // full buffer — drop, never block
		}
	}
}

// C returns the receive channel for this subscription.
func (s *Sub[T]) C() <-chan T { return s.ch }

// Stop unsubscribes and closes the channel, ending the reader's loop. It is
// idempotent. Deregistration happens under the same lock Publish holds, so a
// concurrent Publish never sends on the closed channel.
func (s *Sub[T]) Stop() {
	s.topic.mu.Lock()
	defer s.topic.mu.Unlock()
	if _, ok := s.topic.subs[s]; ok {
		delete(s.topic.subs, s)
		close(s.ch)
	}
}
