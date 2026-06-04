package via

import (
	"context"
	"sync"
)

// inMemoryBackplane is the base in-process Backplane (T1-GO-6). The Store half
// is a per-key CAS cell; the EventLog half is a per-key append log that
// live-tails subscribers via a broadcast channel. All shared state is guarded
// so the whole type is race-clean.
type inMemoryBackplane struct {
	mu     sync.Mutex
	cells  map[string]storeCell
	logs   map[string]*memLog
	closed bool
}

type storeCell struct {
	data []byte
	rev  Rev
}

// memLog is one key's append log. records[i] carries Offset(base+i+1): base is
// the count of compacted-away leading offsets (0 until Compact runs), so an
// offset survives compaction unchanged while the slice keeps a front hole.
// changed is closed (and replaced) on every append to wake blocked subscribers;
// closeCh is closed once when the backplane shuts down.
type memLog struct {
	mu      sync.Mutex
	records []Record
	base    Offset
	changed chan struct{}
	closeCh chan struct{}
	closed  bool
}

// append commits one record under lg.mu, re-checking closed so an Append whose
// logFor passed b.closed but raced a concurrent Close can't silently store a
// record into a stream whose subscribers are already unwinding.
func (lg *memLog) append(key string, record []byte) (Offset, error) {
	lg.mu.Lock()
	defer lg.mu.Unlock()
	if lg.closed {
		return 0, ErrClosed
	}
	off := lg.base + Offset(len(lg.records)) + 1
	// Copy the record so a caller mutating its slice can't corrupt the log.
	data := append([]byte(nil), record...)
	lg.records = append(lg.records, Record{Key: key, Offset: off, Data: data})
	// Broadcast: close the current channel to wake every subscriber, then swap
	// in a fresh one for the next wait.
	close(lg.changed)
	lg.changed = make(chan struct{})
	return off, nil
}

// compact discards every record with Offset < beforeOffset, advancing base so
// the survivors keep their offsets. Clamped to the committed head (never discard
// uncommitted records, never move the head) and idempotent below base+1. The
// in-flight batch a Subscribe goroutine already copied is unaffected — only the
// backing slice is resliced, under lg.mu.
func (lg *memLog) compact(beforeOffset Offset) {
	lg.mu.Lock()
	defer lg.mu.Unlock()
	if head := lg.base + Offset(len(lg.records)); beforeOffset > head+1 {
		beforeOffset = head + 1 // never discard beyond the committed head
	}
	if beforeOffset <= lg.base+1 {
		return // nothing below the current floor to drop
	}
	drop := int(beforeOffset-1) - int(lg.base)
	lg.records = append([]Record(nil), lg.records[drop:]...)
	lg.base = beforeOffset - 1
}

func (lg *memLog) close() {
	lg.mu.Lock()
	defer lg.mu.Unlock()
	lg.closed = true
	close(lg.closeCh)
}

func newInMemoryBackplane() *inMemoryBackplane {
	return &inMemoryBackplane{
		cells: make(map[string]storeCell),
		logs:  make(map[string]*memLog),
	}
}

// logFor returns the per-key log, creating it on first use. Caller must NOT
// hold b.mu's child locks; b.mu is taken here.
func (b *inMemoryBackplane) logFor(key string) (*memLog, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil, false
	}
	lg := b.logs[key]
	if lg == nil {
		lg = &memLog{changed: make(chan struct{}), closeCh: make(chan struct{})}
		b.logs[key] = lg
	}
	return lg, true
}

func (b *inMemoryBackplane) Append(_ context.Context, key string, record []byte) (Offset, error) {
	lg, ok := b.logFor(key)
	if !ok {
		return 0, ErrClosed
	}
	return lg.append(key, record)
}

func (b *inMemoryBackplane) Head(_ context.Context, key string) (Offset, Epoch, error) {
	b.mu.Lock()
	lg := b.logs[key]
	b.mu.Unlock()
	if lg == nil {
		return 0, 0, nil
	}
	lg.mu.Lock()
	defer lg.mu.Unlock()
	return lg.base + Offset(len(lg.records)), 0, nil
}

func (b *inMemoryBackplane) Compact(_ context.Context, key string, beforeOffset Offset) error {
	lg, ok := b.logFor(key)
	if !ok {
		return ErrClosed
	}
	lg.compact(beforeOffset)
	return nil
}

func (b *inMemoryBackplane) Subscribe(ctx context.Context, key string, from Offset) (<-chan Record, error) {
	lg, ok := b.logFor(key)
	if !ok {
		return nil, ErrClosed
	}
	out := make(chan Record)
	go func() {
		defer close(out)
		cursor := from
		for {
			lg.mu.Lock()
			var batch []Record
			// Index of the next undelivered record: cursor-base, clamped to 0 so a
			// subscriber whose cursor fell below a compacted prefix resumes at the
			// lowest retained offset (gap-free, never renumbered) rather than panic.
			start := int(cursor) - int(lg.base)
			if start < 0 {
				start = 0
			}
			if start < len(lg.records) {
				batch = append(batch, lg.records[start:]...)
			}
			wait := lg.changed
			lg.mu.Unlock()

			for _, r := range batch {
				select {
				case out <- r:
					cursor = r.Offset
				case <-ctx.Done():
					return
				case <-lg.closeCh:
					return
				}
			}

			select {
			case <-wait: // a new append occurred; loop to drain it
			case <-ctx.Done():
				return
			case <-lg.closeCh:
				return
			}
		}
	}()
	return out, nil
}

func (b *inMemoryBackplane) LoadSnapshot(_ context.Context, key string) ([]byte, Rev, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cell, ok := b.cells[key]
	if !ok {
		return nil, 0, false, nil
	}
	return cell.data, cell.rev, true, nil
}

func (b *inMemoryBackplane) CAS(_ context.Context, key string, expectedRev Rev, data []byte) (Rev, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	cur := b.cells[key].rev // Rev(0) for an absent key — matches "must not exist"
	if cur != expectedRev {
		return 0, ErrCASConflict
	}
	newRev := cur + 1
	b.cells[key] = storeCell{data: append([]byte(nil), data...), rev: newRev}
	return newRev, nil
}

func (b *inMemoryBackplane) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	b.closed = true
	for _, lg := range b.logs {
		lg.close()
	}
	return nil
}
