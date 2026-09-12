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
	assert.Equal(t, "hi", <-a.C())
	assert.Equal(t, "hi", <-b.C())
}

// One stuck consumer must not stall the publisher (or every other subscriber):
// the broker drops to a full buffer rather than blocking. A framework that lets
// one slow tab freeze the broadcast is unusable.
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
	_, ok := <-s.C()
	assert.False(t, ok, "channel must be closed after Stop")
}

func TestTopic_subsCountsOnlyLiveSubscriptions(t *testing.T) {
	t.Parallel()
	tp := topic.New[int]()
	assert.Zero(t, tp.Subs(), "a fresh topic has no subscribers")

	a, b := tp.Subscribe(), tp.Subscribe()
	assert.Equal(t, 2, tp.Subs())

	a.Stop()
	a.Stop() // idempotent: a second Stop must not double-decrement
	assert.Equal(t, 1, tp.Subs())

	b.Stop()
	assert.Zero(t, tp.Subs())
}
