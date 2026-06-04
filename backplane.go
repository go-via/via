package via

import (
	"context"
	"errors"
	"io"
)

// Offset is an opaque, per-key, monotonically-INCREASING cursor. It is the
// resume primitive: a pod that committed Offset N resumes at "everything after
// N" and provably cannot miss a record. Treat it as OPAQUE — comparable and
// ordered WITHIN one key, never interchangeable across keys or backends, and
// not guaranteed gap-free in a real backend. Offset(0) means "before the first
// record"; Subscribe(from:0) replays all.
type Offset uint64

// Rev is the Store cell's CAS version, DISTINCT from Offset (Store and EventLog
// keep independent counters). Rev(0) means "the cell has never been written".
type Rev uint64

// Epoch is the per-key stream GENERATION. Offsets are only unique and monotone
// WITHIN an epoch; an offset-space reset starts a new epoch. Epoch(0) is the
// genesis generation. (The in-memory backplane never resets, so it stays at 0.)
type Epoch uint64

// Record is one delivered EventLog entry. The runtime, not the backend,
// interprets Data; the backend only moves bytes and assigns Offset.
type Record struct {
	Key    string
	Epoch  Epoch
	Offset Offset
	Data   []byte
}

// Store is the durable per-key current-value cell with compare-and-swap.
type Store interface {
	// LoadSnapshot returns the stored bytes for key and its revision, or
	// ok=false if the key was never written.
	LoadSnapshot(ctx context.Context, key string) (data []byte, rev Rev, ok bool, err error)

	// CAS stores data for key IFF the current revision == expectedRev (Rev(0)
	// means "must not exist yet"). Returns the NEW revision, or ErrCASConflict
	// if the current rev moved — the caller reloads and retries.
	CAS(ctx context.Context, key string, expectedRev Rev, data []byte) (newRev Rev, err error)
}

// EventLog is the durable, ordered, offset-resumable append log.
//
// Guarantees: per-key total order; an Append returns only after the record is
// committed and assigned its Offset; Subscribe(from:K) yields every committed
// record with Offset>K, in order, with no gaps, then live-tails. There is NO
// cross-key ordering — distinct keys are independent aggregates.
type EventLog interface {
	// Append commits one opaque record to key's stream and returns its assigned
	// Offset. Plain append never conflicts.
	Append(ctx context.Context, key string, record []byte) (Offset, error)

	// Subscribe streams records for key with Offset > from (so a pod passes its
	// last-applied offset and resumes exactly after it), then live-tails. The
	// channel closes when ctx is cancelled or the backplane is closed.
	Subscribe(ctx context.Context, key string, from Offset) (<-chan Record, error)

	// Head returns the current highest committed Offset for key and its current
	// Epoch. Offset(0) if the key is empty.
	Head(ctx context.Context, key string) (Offset, Epoch, error)
}

// Backplane is the one interface a backend author implements to make
// app/session-scoped reactive state survive restarts and span a cluster. It
// fuses a durable per-key CAS Store and a durable ordered EventLog, plus a
// graceful drain on App.Shutdown. After Close, Append/Subscribe return
// ErrClosed and never block.
type Backplane interface {
	Store
	EventLog
	io.Closer
}

var (
	// ErrCASConflict is returned by Store.CAS when the current revision moved
	// since the caller's expectedRev — reload and retry.
	ErrCASConflict = errors.New("via: store CAS revision conflict")
	// ErrClosed is returned by a closed backplane's Append/Subscribe.
	ErrClosed = errors.New("via: backplane closed")
)

// InMemory returns the base in-process Backplane: a per-key in-memory event log
// plus a CAS snapshot cell. It is the impl a nil backplane resolves to, so the
// Backplane interface is exercised on every single-pod run. (See T1-GO-6 for
// why this lives in package via rather than a separate package.)
func InMemory() Backplane { return newInMemoryBackplane() }
