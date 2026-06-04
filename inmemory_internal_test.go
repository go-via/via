package via

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Append uses two-phase locking: logFor checks b.closed under b.mu, then a
// separate lg.mu hold stores the record. A Close interleaving between those two
// phases can fully complete — marking the backplane closed and unwinding every
// subscriber — yet the in-flight Append would still commit a record nobody can
// ever read. The per-log append must itself refuse once Close has closed that
// log, so the record is never silently committed into a torn-down stream.
func TestMemLogAppend_refusesOnceClosed(t *testing.T) {
	t.Parallel()
	b := newInMemoryBackplane()
	ctx := context.Background()

	// The key's log must already exist so we exercise the existing-log path.
	_, err := b.Append(ctx, "room", []byte("seed"))
	require.NoError(t, err)

	lg := b.logs["room"]

	// An open log still commits and hands back a monotone offset.
	off, err := lg.append("room", []byte("live"))
	require.NoError(t, err)
	assert.Equal(t, Offset(2), off, "an open log assigns the next offset")

	lg.close() // what Close does to each per-key log

	off, err = lg.append("room", []byte("racer"))
	assert.ErrorIs(t, err, ErrClosed, "a log Close already closed must refuse Append")
	assert.Zero(t, off, "a refused Append hands back no offset")
}
