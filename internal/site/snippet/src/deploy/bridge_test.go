package deploy_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via/topic"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/snippet/src/deploy"
)

// memBus stands in for Redis or NATS: every subscriber on a subject gets
// every payload published to it, across "pods".
type memBus struct {
	mu   sync.Mutex
	subs map[string][]chan []byte
}

func (b *memBus) Publish(_ context.Context, subject string, payload []byte) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, ch := range b.subs[subject] {
		ch <- payload
	}
	return nil
}

func (b *memBus) Subscribe(ctx context.Context, subject string) (<-chan []byte, error) {
	ch := make(chan []byte, 8)
	b.mu.Lock()
	b.subs[subject] = append(b.subs[subject], ch)
	b.mu.Unlock()
	context.AfterFunc(ctx, func() {
		b.mu.Lock()
		defer b.mu.Unlock()
		close(ch)
	})
	return ch, nil
}

func TestRoomPost_reachesTheLocalTopicOfEveryPod(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	bus := &memBus{subs: map[string][]chan []byte{}}

	var subs []*topic.Sub[deploy.Message]
	var pods []*deploy.Room
	for range 2 {
		room := &deploy.Room{ID: "lobby", Bus: bus, Local: topic.New[deploy.Message]()}
		subs = append(subs, room.Local.Subscribe())
		pods = append(pods, room)
	}
	for _, room := range pods {
		go room.Bridge(ctx)
	}
	require.Eventually(t, func() bool {
		bus.mu.Lock()
		defer bus.mu.Unlock()
		return len(bus.subs["room.lobby"]) == 2
	}, time.Second, time.Millisecond)

	require.NoError(t, pods[0].Post(ctx, deploy.Message{Who: "ann", Text: "hi"}))

	for i, sub := range subs {
		select {
		case <-sub.Ready():
		case <-time.After(time.Second):
			t.Fatalf("pod %d never heard the post", i)
		}
		got, _ := sub.Drain()
		assert.Equal(t, []deploy.Message{{Who: "ann", Text: "hi"}}, got)
	}
}
