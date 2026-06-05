package via

import (
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

// When a key's underlying stream is recreated / trimmed-to-empty / restored, its
// offset space restarts at 1 under a NEW epoch. A bare offset high-water-mark
// would then skip every new record (their offsets are <= the old cursor),
// silently freezing the projection. The projector must DETECT the epoch change
// and re-snapshot from genesis so the projection re-converges — emitting
// via.events.epoch_reset.
func TestProjectorReSnapshotsOnEpochReset(t *testing.T) {
	t.Parallel()
	spy := &spyMetrics{}
	var server *httptest.Server
	app := New(WithTestServer(&server), WithMetrics(spy))
	defer server.Close()

	var h StateAppEvents[envEv, []int]
	h.bindWireKey("k")
	h.bindApp(app)
	ls := app.logs["k"]

	// Two records in the original epoch (0) fold normally.
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 0, Offset: 1, Data: goodEnv(t, envEv{N: 1})})
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 0, Offset: 2, Data: goodEnv(t, envEv{N: 2})})
	require.Equal(t, []int{1, 2}, projection(app, "k"), "same-epoch records fold normally")
	require.False(t, spy.saw("via.events.epoch_reset"), "the baseline epoch must not be mistaken for a reset")

	// The stream resets: a NEW epoch (1) whose offsets restart at 1. This MUST
	// re-snapshot from genesis — the projection becomes just the new-epoch event,
	// NOT [1,2,9] (which a bare HWM that appended would produce) and NOT frozen at
	// [1,2] (which a bare HWM that skipped offset 1 <= cursor 2 would produce).
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 1, Offset: 1, Data: goodEnv(t, envEv{N: 9})})
	require.Equal(t, []int{9}, projection(app, "k"), "an epoch reset must re-snapshot from genesis")
	require.Equal(t, Offset(1), logCursor(app, "k"), "cursor restarts in the new epoch")
	require.True(t, spy.saw("via.events.epoch_reset"), "an offset-space reset must emit via.events.epoch_reset")

	// Subsequent new-epoch records fold onto the re-snapshotted projection.
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 1, Offset: 2, Data: goodEnv(t, envEv{N: 10})})
	require.Equal(t, []int{9, 10}, projection(app, "k"), "new-epoch records fold after the reset")

	// A reset is detected by ANY epoch change, not only a forward bump — a
	// restore can roll the epoch backward and still restart the offset space.
	app.projectRecord(ls, "k", Record{Key: "k", Epoch: 0, Offset: 1, Data: goodEnv(t, envEv{N: 5})})
	require.Equal(t, []int{5}, projection(app, "k"), "a backward epoch change also re-snapshots from genesis")
}
