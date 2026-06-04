package via

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

// consumerRetryBackoff paces a side-effect consumer's re-subscribe after a
// handler error, so a persistently-failing handler retries (head-of-line)
// without hot-spinning.
const consumerRetryBackoff = 10 * time.Millisecond

// consumerState is one (name,key) side-effect consumer's per-pod state: the
// committed offset (durable in the Store cell consumerKey) and its CAS rev. The
// committed offset has one writer per pod — the consumer's tailer goroutine.
type consumerState struct {
	once sync.Once
	mu   sync.Mutex

	committed Offset // highest offset whose handler returned nil and was durably committed
	cellRev   Rev    // CAS rev of the committed-offset Store cell
}

func (cs *consumerState) snapshot() (Offset, Rev) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.committed, cs.cellRev
}

// consumerKey is the Store cell holding a named consumer's committed offset for
// one wire key — distinct from val:, snap:, and the raw log key.
func consumerKey(name, wireKey string) string { return "consumer:" + name + ":" + wireKey }

// OnEvent registers a named, offset-tracked, side-effecting consumer over this
// key's event log (send an email on ticket-closed, charge a card). Side effects
// do NOT live in Fold (Fold must stay pure) — they live here, in a separate
// tailer whose committed offset is durable in the Store cell
// "consumer:<name>:<wireKey>" and advanced ONLY after the handler returns nil.
//
// A restart resumes from the committed offset; an event whose effect already ran
// is skipped. Delivery is at-least-once (a crash between effect and commit, or
// two pods both running the consumer, re-runs the handler), so a handler that
// must be exactly-once carries an idempotency key derived from off (e.g. a
// Stripe idempotency-key = wireKey+":"+off). A handler error does NOT advance —
// the record is retried (head-of-line, preserving order); an undecodable record
// is skipped (drop-on-undecodable, like the fold). A registered consumer also
// pins the Compactor floor to its committed offset, so compaction never discards
// an event it has not yet processed.
//
// The handler receives a context.Context scoping the delivery (cancelled when
// the consumer stops delivering), NOT a via *Ctx — a background tailer fires on
// records from any pod, with no originating tab/session. Registration is
// idempotent: the consumer starts once per (name,key) however many times OnEvent
// is called. No-op before Mount (the handle isn't bound yet).
func (l *StateAppEvents[E, V]) OnEvent(name string, fn func(ctx context.Context, ev E, off Offset) error) {
	if l.app == nil {
		return
	}
	app, key := l.app, l.wireKey
	// deliver decodes one record (same envelope path as the fold) and invokes the
	// handler. The bool is whether the consumer may ADVANCE past this record: a
	// clean handle or an undecodable (poison/forward-incompatible) record advances;
	// a handler error does NOT (the record is retried).
	deliver := func(ctx context.Context, data []byte, off Offset) bool {
		ev, err := decodeEvent[E](data)
		if errors.Is(err, ErrForwardIncompatible) {
			// A newer binary wrote this event. Roll-forward-only: do NOT advance —
			// skipping would silently drop the side effect. Block here (the record
			// is retried) until a rolled-forward binary can decode it, mirroring the
			// projector's halt.
			app.metricsOrNoop().Counter("via.consumer.forward_incompatible", "name", name, "key", key)
			return false
		}
		if err != nil {
			// Poison / undecodable: this binary can never process it, so skip +
			// advance rather than wedge the consumer forever.
			app.metricsOrNoop().Counter("via.consumer.undecodable", "name", name, "key", key)
			return true
		}
		if err := fn(ctx, ev, off); err != nil {
			app.metricsOrNoop().Counter("via.consumer.error", "name", name, "key", key)
			return false // retry; do not advance
		}
		return true
	}
	app.registerConsumer(name, key, deliver)
}

// registerConsumer records the (name,key) consumer (cold-loading its committed
// offset from the Store) and starts its tailer exactly once.
func (a *App) registerConsumer(name, key string, deliver func(context.Context, []byte, Offset) bool) {
	ck := consumerKey(name, key)
	a.consumersMu.Lock()
	if a.consumers == nil {
		a.consumers = map[string]*consumerState{}
		a.consumersByKey = map[string][]*consumerState{}
	}
	cs := a.consumers[ck]
	if cs == nil {
		cs = &consumerState{}
		if data, rev, ok, _ := a.backplane.LoadSnapshot(context.Background(), ck); ok {
			var off Offset
			if json.Unmarshal(data, &off) == nil {
				cs.committed = off
				cs.cellRev = rev
			}
		}
		a.consumers[ck] = cs
		a.consumersByKey[key] = append(a.consumersByKey[key], cs)
	}
	a.consumersMu.Unlock()

	cs.once.Do(func() { a.startConsumer(name, key, cs, deliver) })
}

// startConsumer tails the key from the committed offset, delivering each new
// record to the handler and committing on success. On a handler error it
// re-subscribes from the committed offset (head-of-line retry). It exits when
// the backplane closes (Subscribe returns ErrClosed or the channel closes).
func (a *App) startConsumer(name, key string, cs *consumerState, deliver func(context.Context, []byte, Offset) bool) {
	go func() {
		for {
			from, _ := cs.snapshot()
			subCtx, cancel := context.WithCancel(context.Background())
			ch, err := a.backplane.Subscribe(subCtx, key, from)
			if err != nil {
				cancel()
				return // backplane closed
			}
			retry := false
			for rec := range ch {
				if committed, _ := cs.snapshot(); rec.Offset <= committed {
					continue // already handled (a peer advanced the shared offset)
				}
				if deliver(subCtx, rec.Data, rec.Offset) {
					a.commitConsumer(cs, name, key, rec.Offset)
				} else {
					retry = true // handler error → re-subscribe from committed
					break
				}
			}
			cancel()
			if !retry {
				return // channel closed (shutdown), nothing pending
			}
			time.Sleep(consumerRetryBackoff)
		}
	}()
}

// commitConsumer durably advances the consumer's committed offset via CAS. On a
// peer's concurrent advance (ErrCASConflict) it adopts the peer's offset so it
// won't re-handle what another pod already committed.
func (a *App) commitConsumer(cs *consumerState, name, key string, off Offset) {
	b, err := json.Marshal(off)
	if err != nil {
		return
	}
	_, rev := cs.snapshot()
	newRev, err := a.backplane.CAS(context.Background(), consumerKey(name, key), rev, b)
	if errors.Is(err, ErrCASConflict) {
		if data, r, ok, _ := a.backplane.LoadSnapshot(context.Background(), consumerKey(name, key)); ok {
			var peer Offset
			if json.Unmarshal(data, &peer) == nil {
				cs.mu.Lock()
				if peer > cs.committed {
					cs.committed = peer
				}
				cs.cellRev = r
				cs.mu.Unlock()
			}
		}
		return
	}
	if err == nil {
		cs.mu.Lock()
		cs.committed = off
		cs.cellRev = newRev
		cs.mu.Unlock()
	}
}

// minConsumerOffset returns the lowest committed offset among the consumers
// registered for key, and whether any exist. The Compactor floor clamps to it so
// compaction never discards an event a side-effect consumer has not processed.
func (a *App) minConsumerOffset(key string) (Offset, bool) {
	a.consumersMu.Lock()
	defer a.consumersMu.Unlock()
	list := a.consumersByKey[key]
	if len(list) == 0 {
		return 0, false
	}
	min := Offset(^uint64(0) >> 1)
	for _, cs := range list {
		if c, _ := cs.snapshot(); c < min {
			min = c
		}
	}
	return min, true
}
