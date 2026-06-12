package via_test

import (
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/memevents"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/require"
)

// tailMetrics records counter hits and last gauge values keyed by
// "name:label1,value1,...", so tests can observe the tailer_reconnect /
// tailer_up signals through the one public Metrics seam.
type tailMetrics struct {
	mu       sync.Mutex
	counters map[string]int
	gauges   map[string]float64
}

func newTailMetrics() *tailMetrics {
	return &tailMetrics{counters: map[string]int{}, gauges: map[string]float64{}}
}

func tailMetricKey(name string, labels []string) string {
	return name + ":" + strings.Join(labels, ",")
}

func (m *tailMetrics) Counter(name string, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.counters[tailMetricKey(name, labels)]++
}

func (m *tailMetrics) Gauge(name string, v float64, labels ...string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gauges[tailMetricKey(name, labels)] = v
}

func (m *tailMetrics) Histogram(string, float64, ...string) {}

func (m *tailMetrics) counterValue(key string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.counters[key]
}

// counterValueByPrefix sums counters whose key starts with prefix — used for
// per-key feeds (projector:<key>) where the test should not hardcode the key.
func (m *tailMetrics) counterValueByPrefix(prefix string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	total := 0
	for k, v := range m.counters {
		if strings.HasPrefix(k, prefix) {
			total += v
		}
	}
	return total
}

func (m *tailMetrics) gaugeValue(key string) (float64, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.gauges[key]
	return v, ok
}

func (m *tailMetrics) gaugeIs(key string, want float64) bool {
	v, ok := m.gaugeValue(key)
	return ok && v == want
}

func TestBroadcast_resumesAfterBackplaneBlip(t *testing.T) {
	t.Parallel()

	shared := memevents.NewFlaky(via.InMemory())
	mB := newTailMetrics()

	appA := via.New(via.WithBackplane(shared))
	_ = vt.Serve(t, appA)
	via.Mount[broadcastPage](appA, "/")

	appB := via.New(via.WithBackplane(shared), via.WithMetrics(mB))
	serverB := vt.Serve(t, appB)
	via.Mount[broadcastPage](appB, "/")

	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	const pre = `console.log("pre-blip")`
	appA.Broadcast(pre)
	vt.AwaitFrame(t, framesB, 2*time.Second, pre)

	shared.Blip()

	// tailer_reconnect moves only AFTER the fresh subscription is live, so a
	// broadcast issued after observing it cannot fall into the gap.
	require.Eventually(t, func() bool {
		return mB.counterValue("via.backplane.tailer_reconnect:feed,broadcast") >= 1
	}, 2*time.Second, 5*time.Millisecond,
		"pod B's broadcast tailer must re-subscribe after the blip")

	const post = `console.log("post-blip")`
	appA.Broadcast(post)
	vt.AwaitFrame(t, framesB, 2*time.Second, post)

	require.True(t, mB.gaugeIs("via.backplane.tailer_up:feed,broadcast", 1),
		"tailer_up must report 1 once the broadcast tailer is re-subscribed")
}

func TestBroadcast_retriesBootTimeSubscribeFailure(t *testing.T) {
	t.Parallel()

	flaky := memevents.NewFlaky(via.InMemory())
	// Armed BEFORE via.New: the broadcast tailer subscribes at construction,
	// so its first attempts hit the injected failures.
	flaky.FailSubscribes(3)
	m := newTailMetrics()

	app := via.New(via.WithBackplane(flaky), via.WithMetrics(m))
	server := vt.Serve(t, app)
	via.Mount[broadcastPage](app, "/")

	c := vt.NewClient(t, server, "/")
	frames, cancel := c.SSEReady()
	defer cancel()

	require.Eventually(t, func() bool {
		return m.gaugeIs("via.backplane.tailer_up:feed,broadcast", 1)
	}, 2*time.Second, 5*time.Millisecond,
		"the broadcast tailer must retry boot-time Subscribe failures until one succeeds")

	const msg = `console.log("after boot retry")`
	app.Broadcast(msg)
	vt.AwaitFrame(t, frames, 2*time.Second, msg)
}

func TestStateApp_convergesAfterChangesTailerReconnect(t *testing.T) {
	t.Parallel()

	shared := memevents.NewFlaky(via.InMemory())
	// Reconcile disabled: only the changes tailer can carry A's write to B,
	// so post-blip convergence proves the tailer reconnected.
	off := via.WithReconcileInterval(0)
	mB := newTailMetrics()

	appA := via.New(via.WithBackplane(shared), off)
	serverA := vt.Serve(t, appA)
	via.Mount[appCounterPage](appA, "/")

	appB := via.New(via.WithBackplane(shared), off, via.WithMetrics(mB))
	serverB := vt.Serve(t, appB)
	via.Mount[appCounterPage](appB, "/")

	a := vt.NewClient(t, serverA, "/")
	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	require.Equal(t, http.StatusOK, a.Action("Bump").Fire())
	vt.AwaitFrame(t, framesB, 2*time.Second, `<span id="visits">1</span>`)

	shared.Blip()
	require.Eventually(t, func() bool {
		return mB.counterValue("via.backplane.tailer_reconnect:feed,changes") >= 1
	}, 2*time.Second, 5*time.Millisecond,
		"pod B's changes tailer must re-subscribe after the blip")

	require.Equal(t, http.StatusOK, a.Action("Bump").Fire())
	vt.AwaitFrame(t, framesB, 2*time.Second, `<span id="visits">2</span>`)

	require.True(t, mB.gaugeIs("via.backplane.tailer_up:feed,changes", 1),
		"tailer_up must report 1 once the changes tailer is re-subscribed")
}

func TestStateAppEvents_projectorResumesAfterBackplaneBlip(t *testing.T) {
	t.Parallel()

	shared := memevents.NewFlaky(via.InMemory())
	mB := newTailMetrics()

	appA := via.New(via.WithBackplane(shared))
	serverA := vt.Serve(t, appA)
	via.Mount[feedPage](appA, "/")

	appB := via.New(via.WithBackplane(shared), via.WithMetrics(mB))
	serverB := vt.Serve(t, appB)
	via.Mount[feedPage](appB, "/")

	a := vt.NewClient(t, serverA, "/")
	b := vt.NewClient(t, serverB, "/")
	framesB, cancelB := b.SSEReady()
	defer cancelB()

	require.Equal(t, http.StatusOK, a.Action("Add").Fire())
	vt.AwaitFrame(t, framesB, 2*time.Second, `<div id="feed">hello</div>`)

	shared.Blip()
	require.Eventually(t, func() bool {
		return mB.counterValueByPrefix("via.backplane.tailer_reconnect:feed,projector:") >= 1
	}, 2*time.Second, 5*time.Millisecond,
		"pod B's projector must re-subscribe after the blip")

	require.Equal(t, http.StatusOK, a.Action("Add").Fire())
	vt.AwaitFrame(t, framesB, 2*time.Second, `<div id="feed">hello,hello</div>`)
}
