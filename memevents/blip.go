package memevents

import (
	"context"
	"fmt"
	"sync"

	"github.com/go-via/via"
)

// Flaky decorates a [via.Backplane] with faults a test triggers explicitly,
// where [Faulty] injects them by count. Blip severs every live Subscribe
// channel without closing the backplane, and FailSubscribes rejects the next
// n Subscribe calls — together they reproduce the two observable symptoms of
// a real backend outage (mid-stream disconnect, boot-time subscribe error)
// deterministically, with no infrastructure. The wrapped Backplane is
// embedded, so Store/Append/Head/Close delegate unchanged.
type Flaky struct {
	via.Backplane
	mu       sync.Mutex
	failures int
	drops    []chan struct{}
}

// NewFlaky wraps bp. Flaky carries a mutex and live-subscription registry, so
// it is constructed by pointer rather than as a value literal like Faulty.
func NewFlaky(bp via.Backplane) *Flaky { return &Flaky{Backplane: bp} }

// FailSubscribes arms the decorator to reject the next n Subscribe calls with
// an injected error, simulating a backend that is unreachable at boot.
func (f *Flaky) FailSubscribes(n int) {
	f.mu.Lock()
	f.failures = n
	f.mu.Unlock()
}

// Blip closes every live subscription channel WITHOUT closing the backplane —
// a transient drop. The underlying streams are intact, so a consumer that
// re-subscribes from its cursor resumes gap-free.
func (f *Flaky) Blip() {
	f.mu.Lock()
	drops := f.drops
	f.drops = nil
	f.mu.Unlock()
	for _, d := range drops {
		close(d)
	}
}

// Subscribe wraps the underlying stream so Blip can sever it. The wrapper
// goroutine cancels the underlying subscription and closes its channel on any
// exit (ctx cancel, backplane Close, or a Blip), so it cannot leak the
// underlying tailer. Severed entries stay in the registry until the next
// Blip clears it — closing an already-dead drop channel is harmless and the
// decorator only lives for a test's duration.
func (f *Flaky) Subscribe(ctx context.Context, key string, from via.Offset) (<-chan via.Record, error) {
	f.mu.Lock()
	if f.failures > 0 {
		f.failures--
		f.mu.Unlock()
		return nil, fmt.Errorf("memevents: injected subscribe failure")
	}
	drop := make(chan struct{})
	f.drops = append(f.drops, drop)
	f.mu.Unlock()

	subCtx, cancel := context.WithCancel(ctx)
	in, err := f.Backplane.Subscribe(subCtx, key, from)
	if err != nil {
		cancel()
		return nil, err
	}
	out := make(chan via.Record)
	go func() {
		defer close(out)
		defer cancel() // tear down the underlying tailer on any return
		for {
			select {
			case r, ok := <-in:
				if !ok {
					return
				}
				select {
				case out <- r:
				case <-drop:
					return
				case <-ctx.Done():
					return
				}
			case <-drop:
				return
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}
