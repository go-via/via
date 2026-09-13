package topic_test

import (
	"testing"
	"time"

	"github.com/go-via/via/topic"
	"github.com/stretchr/testify/assert"
)

// A published value reaches every current subscriber — that is the fan-out the
// multi-user case is built on.
func TestTopic_publishReachesEverySubscriber(t *testing.T) {
	t.Parallel()
	tp := topic.New[string]()
	a, b := tp.Subscribe(), tp.Subscribe()
	tp.Publish("hi")
	<-a.Ready()
	<-b.Ready()
	av, aok := a.Drain()
	bv, bok := b.Drain()
	assert.Equal(t, []string{"hi"}, av)
	assert.Equal(t, []string{"hi"}, bv)
	assert.True(t, aok)
	assert.True(t, bok)
}

// One stuck consumer must not stall the publisher (or every other subscriber):
// Publish only ever appends to a queue. A framework that lets one slow tab
// freeze the broadcast is unusable.
func TestTopic_slowSubscriberDoesNotBlockThePublisher(t *testing.T) {
	t.Parallel()
	tp := topic.New[int]()
	tp.Subscribe() // never drained
	done := make(chan struct{})
	go func() {
		for i := range 10000 {
			tp.Publish(i)
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("publisher blocked on a slow subscriber")
	}
}

// Stop deregisters a subscriber and closes its channel, so later publishes never
// reach it and its reader loop ends.
func TestTopic_stopDeregistersAndClosesChannel(t *testing.T) {
	t.Parallel()
	tp := topic.New[string]()
	s := tp.Subscribe()
	s.Stop()
	tp.Publish("after-stop")
	_, open := <-s.Ready()
	assert.False(t, open, "Ready must be closed after Stop")
	batch, ok := s.Drain()
	assert.Empty(t, batch, "a publish after Stop must not reach the subscriber")
	assert.False(t, ok, "Drain must report the subscription finished")
}

func TestTopic_subsCountsOnlyLiveSubscriptions(t *testing.T) {
	t.Parallel()
	tp := topic.New[int]()
	assert.Zero(t, tp.NumSubs(), "a fresh topic has no subscribers")

	a, b := tp.Subscribe(), tp.Subscribe()
	assert.Equal(t, 2, tp.NumSubs())

	a.Stop()
	a.Stop() // idempotent: a second Stop must not double-decrement
	assert.Equal(t, 1, tp.NumSubs())

	b.Stop()
	assert.Zero(t, tp.NumSubs())
}

// A burst far larger than any per-subscriber buffer must still deliver every
// value to every subscriber. A drop-on-full fan-out loses ~87% of a burst this
// size, which is why delivery does not drop.
func TestTopic_burstIsLosslessForEverySubscriber(t *testing.T) {
	t.Parallel()
	const subs, msgs = 100, 1000
	tp := topic.New[int]()
	ss := make([]*topic.Sub[int], subs)
	for i := range ss {
		ss[i] = tp.Subscribe()
	}
	for i := range msgs {
		tp.Publish(i)
	}
	for i, s := range ss {
		got, _ := s.Drain()
		if !assert.Len(t, got, msgs, "subscriber %d lost values", i) {
			t.FailNow()
		}
		for j, v := range got {
			if v != j {
				t.Fatalf("subscriber %d out of order at %d: got %d", i, j, v)
			}
		}
	}
}

// Loss past the queue limit is the one documented failure mode, and it must be
// counted rather than silent.
func TestTopic_overLimitDropsAreCounted(t *testing.T) {
	t.Parallel()
	tp := topic.New[int]()
	s := tp.SubscribeLimit(4)
	for i := range 10 {
		tp.Publish(i)
	}
	assert.Equal(t, 6, s.Dropped())
	batch, _ := s.Drain()
	assert.Equal(t, []int{0, 1, 2, 3}, batch, "the oldest values survive")
	assert.Zero(t, tp.Subscribe().Dropped(), "a healthy subscriber drops nothing")
}

// Values queued before Stop stay drainable: a reader woken by the close must
// still see everything published before it.
func TestTopic_stopKeepsAlreadyQueuedValues(t *testing.T) {
	t.Parallel()
	tp := topic.New[string]()
	s := tp.Subscribe()
	tp.Publish("a")
	s.Stop()
	batch, ok := s.Drain()
	assert.Equal(t, []string{"a"}, batch)
	assert.True(t, ok, "a non-empty final batch must still be reported deliverable")
	_, ok = s.Drain()
	assert.False(t, ok)
}

// BenchmarkTopic_burstFanOut quantifies the fix: delivered/published under the
// exact burst that made the old drop-on-full broker lose ~87%.
func BenchmarkTopic_burstFanOut(b *testing.B) {
	const subs, msgs = 100, 1000
	var delivered, published int64
	for b.Loop() {
		tp := topic.New[int]()
		ss := make([]*topic.Sub[int], subs)
		for i := range ss {
			ss[i] = tp.Subscribe()
		}
		for i := range msgs {
			tp.Publish(i)
		}
		published += subs * msgs
		for _, s := range ss {
			got, _ := s.Drain()
			delivered += int64(len(got))
		}
	}
	b.ReportMetric(float64(delivered)/float64(published)*100, "%delivered")
}
