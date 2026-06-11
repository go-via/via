package via

import (
	"context"
	"encoding/json"
	"errors"
	"math/rand"
	"sync"
	"time"
)

// Default retry backoff bounds for a side-effect consumer's re-subscribe after a
// handler error: exponential from base to max, with jitter, so a persistently-
// failing handler retries (head-of-line) without hot-spinning.
const (
	defaultRetryBackoffBase = 10 * time.Millisecond
	defaultRetryBackoffMax  = 30 * time.Second
)

// stuckWarnThreshold is the per-pod attempt count at which a blocking
// (still-retrying) consumer emits a one-shot WARN, so a floor-pin from a
// permanently-failing handler is observable rather than silent.
const stuckWarnThreshold = 8

// consumerConfig is the per-consumer policy assembled from ConsumerOption.
type consumerConfig struct {
	maxAttempts int // 0 = block forever (never skip a record); >0 = poison after N handler-error attempts
	backoffBase time.Duration
	backoffMax  time.Duration
	deadLetter  func(ctx context.Context, key string, off Offset, data []byte, cause error) error
}

func defaultConsumerConfig() consumerConfig {
	return consumerConfig{
		maxAttempts: 0,
		backoffBase: defaultRetryBackoffBase,
		backoffMax:  defaultRetryBackoffMax,
	}
}

// ConsumerOption tunes a single OnEvent consumer's poison/retry policy.
type ConsumerOption func(*consumerConfig)

// WithMaxAttempts opts a consumer into skipping a poison record. After n
// consecutive handler-error attempts on the same record the consumer treats it
// as poison (dead-letters it if a hook is set, otherwise drops it) and advances
// past it, un-wedging the consumer and unpinning the Compactor floor.
//
// The DEFAULT is 0 = block forever: a persistently-failing handler keeps the
// record head-of-line and never drops the side effect (today's zero-data-loss
// semantics). Skipping is strictly opt-in via n > 0. A forward-incompatible
// record (a newer binary wrote it) is NEVER poisoned regardless of n — it always
// blocks, since it is a rollback guard rather than a bad record.
func WithMaxAttempts(n int) ConsumerOption {
	return func(c *consumerConfig) { c.maxAttempts = n }
}

// WithRetryBackoff sets the exponential-backoff bounds (with jitter) the
// consumer waits between head-of-line retries of a failing handler. Defaults are
// 10ms → 30s.
func WithRetryBackoff(base, max time.Duration) ConsumerOption {
	return func(c *consumerConfig) {
		if base > 0 {
			c.backoffBase = base
		}
		if max > 0 {
			c.backoffMax = max
		}
	}
}

// WithDeadLetter registers a hook invoked when a record is about to be poisoned
// (only reachable with WithMaxAttempts(n>0)). It receives the wire key, offset,
// raw record bytes, and the last handler error. If it returns nil the consumer
// commits past the record; if it returns an error the consumer does NOT advance
// and keeps retrying, so a record the operator opted to dead-letter is never
// silently lost when the sink is unavailable.
func WithDeadLetter(fn func(ctx context.Context, key string, off Offset, data []byte, cause error) error) ConsumerOption {
	return func(c *consumerConfig) { c.deadLetter = fn }
}

// deliverOutcome is the result of attempting to deliver one record.
type deliverOutcome int

const (
	// outcomeAdvance: the record was handled (or cleanly skipped) — commit + move on.
	outcomeAdvance deliverOutcome = iota
	// outcomeRetry: the handler returned an error — retry head-of-line. This is
	// the only outcome eligible for poison/skip under WithMaxAttempts.
	outcomeRetry
	// outcomeBlock: the record is forward-incompatible (a newer binary wrote it).
	// Block forever; NEVER count toward poison attempts — it is a rollback guard.
	outcomeBlock
)

// consumerState is one (name,key) side-effect consumer's per-pod state: the
// committed offset (durable in the Store cell consumerKey) and its CAS rev. The
// committed offset has one writer per pod — the consumer's tailer goroutine.
type consumerState struct {
	once sync.Once
	mu   sync.Mutex

	committed Offset // highest offset whose handler returned nil and was durably committed
	cellRev   Rev    // CAS rev of the committed-offset Store cell

	attempts    int  // per-pod consecutive handler-error attempts on the current head-of-line record
	warnedStuck bool // whether the one-shot "stuck" WARN has fired for the current attempt run
}

func (cs *consumerState) snapshot() (Offset, Rev) {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.committed, cs.cellRev
}

// resetAttempts clears the per-pod retry counter after any advance.
func (cs *consumerState) resetAttempts() {
	cs.mu.Lock()
	cs.attempts = 0
	cs.warnedStuck = false
	cs.mu.Unlock()
}

// attemptCount returns the current per-pod retry attempt count.
func (cs *consumerState) attemptCount() int {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.attempts
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
// Stripe idempotency-key = wireKey+":"+off):
//
//	func (t *Tickets) OnInit(ctx *via.Ctx) {
//	    t.Events.OnEvent("notify", func(ctx context.Context, ev TicketEvent, off via.Offset) error {
//	        if ev.Kind != Closed {
//	            return nil // not interested → committed, advance past it
//	        }
//	        // idempotency key = key+offset: a redelivery (crash/two pods) is a no-op
//	        return mailer.Send(ctx, ev.Email, "Closed", mail.IdempotencyKey(t.Events.Key()+":"+strconv.FormatUint(uint64(off),10)))
//	    })
//	}
//
// Error surface, by outcome:
//   - handler returns nil      → commit + advance (effect ran exactly once on this pod)
//   - handler returns an error → DO NOT advance; re-subscribe from committed and
//     retry head-of-line (preserves order) with exponential backoff + jitter;
//     via.consumer.error each attempt; via.consumer.stuck gauge tracks the
//     attempt count so a floor-pin is observable. With WithMaxAttempts(n>0) the
//     record is treated as poison after n attempts (dead-lettered or dropped) and
//     the consumer advances; the DEFAULT (maxAttempts 0) blocks forever, never
//     dropping a side effect.
//   - undecodable record       → skip + advance (drop-on-undecodable, like the
//     fold); via.consumer.undecodable.
//   - forward-incompatible      → BLOCK (do not advance), so a rolled-back deploy
//     never silently skips an event it cannot yet understand;
//     via.consumer.forward_incompatible. NEVER poisoned regardless of WithMaxAttempts.
//
// A registered consumer also pins the Compactor floor to its committed offset,
// so compaction never discards an event it has not yet processed.
//
// The handler receives a context.Context scoping the delivery (cancelled when
// the consumer stops delivering), NOT a via *Ctx — a background tailer fires on
// records from any pod, with no originating tab/session. Registration is
// idempotent: the consumer starts once per (name,key) however many times OnEvent
// is called. No-op before Mount (the handle isn't bound yet).
func (l *StateAppEvents[E, V]) OnEvent(name string, fn func(ctx context.Context, ev E, off Offset) error, opts ...ConsumerOption) {
	if l.app == nil {
		return
	}
	app, key := l.app, l.wireKey
	cfg := defaultConsumerConfig()
	for _, opt := range opts {
		opt(&cfg)
	}
	// deliver decodes one record (same envelope path as the fold) and invokes the
	// handler, reporting one of three outcomes: advance (clean handle or clean
	// skip), retry (handler error — poison-eligible, carries the error), or block
	// (forward-incompatible — never poison). When the handler errors it returns
	// outcomeRetry plus the cause so the tailer can dead-letter/skip under policy.
	deliver := func(ctx context.Context, data []byte, off Offset) (deliverOutcome, error) {
		ev, err := decodeEvent[E](data, app.eventDecryptor())
		if errors.Is(err, ErrForwardIncompatible) {
			// A newer binary wrote this event. Roll-forward-only: do NOT advance —
			// skipping would silently drop the side effect. Block here until a
			// rolled-forward binary can decode it, mirroring the projector's halt.
			// NEVER poison: this is a rollback guard, not a bad record.
			app.metricsOrNoop().Counter("via.consumer.forward_incompatible", "name", name, "key", key)
			return outcomeBlock, nil
		}
		if errors.Is(err, ErrErased) {
			// The subject was erased (crypto-shred): the effect can never run on
			// shredded data. Skip + advance (like a poison record) — do NOT block,
			// or erasure would wedge the consumer forever.
			app.metricsOrNoop().Counter("via.consumer.erased", "name", name, "key", key)
			return outcomeAdvance, nil
		}
		if err != nil {
			// Poison / undecodable: this binary can never process it, so skip +
			// advance rather than wedge the consumer forever.
			app.metricsOrNoop().Counter("via.consumer.undecodable", "name", name, "key", key)
			return outcomeAdvance, nil
		}
		if err := fn(ctx, ev, off); err != nil {
			app.metricsOrNoop().Counter("via.consumer.error", "name", name, "key", key)
			return outcomeRetry, err // poison-eligible; do not advance yet
		}
		return outcomeAdvance, nil
	}
	app.registerConsumer(name, key, cfg, deliver)
}

// registerConsumer records the (name,key) consumer (cold-loading its committed
// offset from the Store) and starts its tailer exactly once.
func (a *App) registerConsumer(name, key string, cfg consumerConfig, deliver func(context.Context, []byte, Offset) (deliverOutcome, error)) {
	ck := consumerKey(name, key)
	a.consumersMu.Lock()
	if a.consumers == nil {
		a.consumers = map[string]*consumerState{}
		a.consumersByKey = map[string][]*consumerState{}
	}
	cs := a.consumers[ck]
	if cs == nil {
		cs = &consumerState{}
		if data, rev, ok, _ := a.backplane.LoadSnapshot(a.backplaneCtx, ck); ok {
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

	cs.once.Do(func() { a.startConsumer(name, key, cs, cfg, deliver) })
}

// startConsumer tails the key from the committed offset, delivering each new
// record to the handler and committing on success. On a handler error it
// re-subscribes from the committed offset (head-of-line retry) with exponential
// backoff + jitter, optionally poisoning the record under WithMaxAttempts. It
// exits when the backplane closes (Subscribe returns ErrClosed or the channel
// closes).
func (a *App) startConsumer(name, key string, cs *consumerState, cfg consumerConfig, deliver func(context.Context, []byte, Offset) (deliverOutcome, error)) {
	a.bgWG.Add(1)
	go func() {
		defer a.bgWG.Done()
		for {
			from, _ := cs.snapshot()
			subCtx, cancel := context.WithCancel(a.backplaneCtx)
			ch, err := a.backplane.Subscribe(subCtx, key, from)
			if err != nil {
				cancel()
				a.logWarn(nil, "via: backplane subscribe failed for consumer %q key %q: %v", name, key, err)
				return // backplane closed
			}
			retry := false
		consume:
			for {
				var rec Record
				var ok bool
				// Wake on backplaneDone so teardown is prompt even against a
				// backend that does not close the channel on ctx cancel.
				select {
				case rec, ok = <-ch:
					if !ok {
						break consume // channel closed: transient disconnect or graceful stop
					}
				case <-a.backplaneDone:
					cancel()
					return // graceful stop: don't wait for a slow backend to close ch
				}
				if committed, _ := cs.snapshot(); rec.Offset <= committed {
					continue // already handled (a peer advanced the shared offset)
				}
				outcome, cause := deliver(subCtx, rec.Data, rec.Offset)
				switch outcome {
				case outcomeAdvance:
					a.commitConsumer(cs, name, key, rec.Offset)
					cs.resetAttempts()
				case outcomeBlock:
					// Forward-incompatible: never poison, block forever.
					retry = true
					break consume
				case outcomeRetry:
					if a.handleRetry(cs, name, key, cfg, rec, cause) {
						continue // poisoned (dead-lettered or dropped) → advanced past it
					}
					retry = true
					break consume
				}
			}
			cancel()
			if !retry && a.shuttingDown() {
				return // graceful stop: backplane closing, nothing pending
			}
			// Either a handler error (retry) OR a transient disconnect (channel
			// closed while still running) → re-subscribe from the committed offset
			// and rehydrate. At-least-once is preserved: a dropped subscription must
			// never silently drop side effects.
			a.sleepBackoff(cfg, cs.attemptCount())
		}
	}()
}

// handleRetry records one handler-error attempt and decides whether the record
// is now poison (advance past it, returning true) or should keep blocking
// (return false). It emits via.consumer.stuck each attempt and a one-shot WARN
// once attempts cross the stuck threshold, so a floor-pin is loud even while
// blocking.
func (a *App) handleRetry(cs *consumerState, name, key string, cfg consumerConfig, rec Record, cause error) bool {
	cs.mu.Lock()
	cs.attempts++
	attempts := cs.attempts
	crossedThreshold := !cs.warnedStuck && attempts >= stuckWarnThreshold
	if crossedThreshold {
		cs.warnedStuck = true
	}
	cs.mu.Unlock()

	a.metricsOrNoop().Gauge("via.consumer.stuck", float64(attempts), "name", name, "key", key)
	if crossedThreshold {
		a.logWarn(nil, "via: consumer %q key %q stuck on offset %d after %d attempts (floor pinned): %v",
			name, key, rec.Offset, attempts, cause)
	}

	if cfg.maxAttempts <= 0 || attempts < cfg.maxAttempts {
		return false // block forever (default) or not yet at the skip threshold
	}

	// Poison: maxAttempts reached on a handler error.
	if cfg.deadLetter != nil {
		if err := cfg.deadLetter(a.backplaneCtx, key, rec.Offset, rec.Data, cause); err != nil {
			// The operator opted to dead-letter but the sink is unavailable: do NOT
			// advance — keep retrying rather than silently lose the record.
			return false
		}
	}
	a.metricsOrNoop().Counter("via.consumer.poisoned", "name", name, "key", key)
	a.logWarn(nil, "via: consumer %q key %q poisoned offset %d after %d attempts: %v",
		name, key, rec.Offset, attempts, cause)
	a.commitConsumer(cs, name, key, rec.Offset)
	cs.resetAttempts()
	return true
}

// sleepBackoff waits an exponentially-growing, jittered interval before the next
// re-subscribe. attempts<=1 (a transient disconnect, or the first handler error)
// uses the base delay.
func (a *App) sleepBackoff(cfg consumerConfig, attempts int) {
	d := cfg.backoffBase
	for i := 1; i < attempts && d < cfg.backoffMax; i++ {
		d *= 2
	}
	if d > cfg.backoffMax {
		d = cfg.backoffMax
	}
	// Full jitter in [d/2, d]: spreads a thundering herd of pods retrying the same
	// poison record without ever busy-waiting below half the computed delay.
	if d > 0 {
		half := d / 2
		d = half + time.Duration(rand.Int63n(int64(half)+1))
	}
	select {
	case <-time.After(d):
	case <-a.backplaneCtx.Done():
	}
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
	newRev, err := a.backplane.CAS(a.backplaneCtx, consumerKey(name, key), rev, b)
	if errors.Is(err, ErrCASConflict) {
		if data, r, ok, _ := a.backplane.LoadSnapshot(a.backplaneCtx, consumerKey(name, key)); ok {
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
