package via_test

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/require"
)

// The value-shaped counterpart of the StateAppEvents cross-pod keystone: a
// value-shaped StateApp.Update on pod A must converge on pod B when both share
// one backplane. Today StateApp is pod-local (its own appStore), so B never sees
// A's write — this proves the Store-as-source-of-truth value path. (appCounterPage
// is defined in stateapp_test.go: a StateAppNum[int] "visits" + Bump action +
// a <span id="visits"> view.)
func TestStateAppConvergesAcrossPods(t *testing.T) {
	t.Parallel()

	shared := via.InMemory()

	var serverA, serverB *httptest.Server
	appA := via.New(via.WithTestServer(&serverA), via.WithBackplane(shared))
	via.Mount[appCounterPage](appA, "/")
	defer serverA.Close()

	appB := via.New(via.WithTestServer(&serverB), via.WithBackplane(shared))
	via.Mount[appCounterPage](appB, "/")
	defer serverB.Close()

	a := vt.NewClient(t, serverA, "/")
	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	require.Equal(t, 200, a.Action("Bump").Fire())

	// Pod B's changes-tailer re-pulls the Store cell A wrote and re-renders B's
	// live tab — value-shaped convergence with no shared process state.
	vt.AwaitFrame(t, framesB, 2*time.Second, `<span id="visits">1</span>`)

	// A fresh reader on pod B also sees the converged value.
	require.Eventually(t, func() bool {
		return strings.Contains(vt.NewClient(t, serverB, "/").HTML(), `<span id="visits">1</span>`)
	}, 2*time.Second, 20*time.Millisecond, "a fresh reader on pod B must see pod A's write")
}
