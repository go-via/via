package via

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The payoff: a fresh pod sharing a backplane whose prefix has been compacted
// still cold-starts to the full projection — it seeds from the snapshot and
// never needs the discarded events.
func TestFreshProjectorColdStartsAfterPrefixCompacted(t *testing.T) {
	t.Parallel()
	var server *httptest.Server
	app := New(WithTestServer(&server), WithSnapshotInterval(1))
	defer server.Close()
	ctx := context.Background()

	var hA StateAppEvents[envEv, []int]
	hA.bindWireKey("k")
	hA.bindApp(app)
	for i := 1; i <= 5; i++ {
		_, err := app.backplane.Append(ctx, "k", goodEnv(t, envEv{N: i}))
		require.NoError(t, err)
	}
	require.Eventually(t, func() bool {
		return lowestRetainedOffset(t, app.backplane, "k") > 1
	}, 2*time.Second, 10*time.Millisecond, "the prefix must be compacted before the fresh pod starts")

	appB := New(WithTestServer(&server), WithBackplane(app.backplane))
	var hB StateAppEvents[envEv, []int]
	hB.bindWireKey("k")
	hB.bindApp(appB)

	require.Eventually(t, func() bool {
		p := projection(appB, "k")
		return len(p) == 5 && p[0] == 1 && p[4] == 5
	}, 2*time.Second, 10*time.Millisecond,
		"the fresh pod must reach the full projection from the snapshot despite compaction")
}
