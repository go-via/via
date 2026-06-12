package via

// Metrics is the optional integration seam for ops observability. via
// emits structured events at well-known names; the implementation
// routes them to whatever backend the operator picked (Prometheus, OTel,
// statsd, expvar, …). Install via [WithMetrics].
//
// The default implementation is [noopMetrics], which discards every
// event — apps that don't configure metrics pay no allocation cost.
//
// Event catalogue (every name via emits; keep in sync with the call sites):
//
// Actions & render:
//   - "via.action.total"      counter, labels: method
//   - "via.action.latency"    histogram (seconds), labels: method
//   - "via.render.total"      counter, labels: route
//
// SSE lifecycle:
//   - "via.sse.connect"       counter — each successful handshake
//   - "via.sse.disconnect"    counter, labels: reason ("client", "shutdown")
//   - "via.sse.recover"       counter, labels: mode ("reload", "rebootstrap")
//   - "via.sse.resync"        counter — a tab re-synced its signal state
//
// Tab (Ctx) lifecycle:
//   - "via.ctx.live"          gauge — current registered tab count
//   - "via.ctx.reap"          counter, labels: reason ("ttl", "shutdown")
//
// Session:
//   - "via.session.mismatch"  counter — an action/SSE handshake's bound
//     session no longer matched the request cookie (403); usually two
//     co-located via apps clobbering one another's session cookie
//
// Event-log projection (StateAppEvents projector), all labelled by key:
//   - "via.events.epoch_reset"           counter — stream generation reset, re-folded
//   - "via.events.forward_incompatible"  counter — record from a newer binary; key halted
//   - "via.events.erased"                counter — crypto-shred-erased payload, skipped
//   - "via.events.undecodable"           counter — poison record, skipped
//   - "via.events.compaction_reseed"     counter — gap recovered from a bridging snapshot
//   - "via.events.compaction_gap_halt"   counter — unbridgeable compacted gap; key halted
//
// Fold-divergence canary:
//   - "via.fold.offset"       gauge, labels: key — applied offset after each fold
//   - "via.fold.digest"       gauge, labels: key, offset — projection digest at that offset
//   - "via.fold.divergence"   counter, labels: key — WithFoldVerify saw an impure fold
//
// Snapshot cold-start:
//   - "via.snapshot.unbridgeable"  counter, labels: key — compacted snapshot, no migration; halted
//   - "via.snapshot.erasure_halt"  counter, labels: key — compacted snapshot invalidated by erasure; halted
//
// Backplane tailers (the shared changes and broadcast feeds, plus each
// StateAppEvents projector), labelled by feed — "changes", "broadcast", or
// "projector:<key>":
//   - "via.backplane.tailer_reconnect"  counter — a tailer re-established its
//     subscription after a transient disconnect or failed subscribe attempt
//     (emitted once the fresh subscription is live)
//   - "via.backplane.tailer_up"         gauge 0/1 — whether the tailer
//     currently holds a live subscription
//
// Side-effect consumers (OnEvent), all labelled by name, key:
//   - "via.consumer.forward_incompatible"  counter — record from a newer binary
//   - "via.consumer.erased"                counter — erased payload, skipped
//   - "via.consumer.undecodable"           counter — poison record, skipped
//   - "via.consumer.error"                 counter — the consumer callback returned an error
//
// Labels are passed as flat key,value pairs to keep the call site
// allocation-free in the noop path.
type Metrics interface {
	Counter(name string, labels ...string)
	Gauge(name string, value float64, labels ...string)
	Histogram(name string, value float64, labels ...string)
}

// Teardown reasons. They label the "reason" of via.sse.disconnect (how a
// live SSE loop exits) and/or via.ctx.reap (server-side Ctx reclamation).
const (
	// disconnectClient: the client went away on its own — request-context
	// cancel, a failed keepalive/patch write, or the tab-close beacon.
	// Labels via.sse.disconnect only (a client close is not a reap).
	disconnectClient = "client"
	// disconnectShutdown: App.Shutdown tore the Ctx down. Labels BOTH
	// via.sse.disconnect (the woken loop) and via.ctx.reap (the teardown).
	disconnectShutdown = "shutdown"
	// disconnectTTL: the idle-TTL sweep reclaimed a stream-less Ctx. Labels
	// via.ctx.reap — a connected stream is never TTL-swept, so this reason
	// never reaches via.sse.disconnect.
	disconnectTTL = "ttl"
)

// noopMetrics is the default backend. Every method is a no-op so apps
// that haven't configured Metrics pay nothing on the hot path.
type noopMetrics struct{}

func (noopMetrics) Counter(string, ...string)            {}
func (noopMetrics) Gauge(string, float64, ...string)     {}
func (noopMetrics) Histogram(string, float64, ...string) {}

// metricsOrNoop returns the configured backend or the noop fallback.
// Called on the hot path; kept tiny so it inlines.
func (a *App) metricsOrNoop() Metrics {
	if a.cfg.metrics == nil {
		return noopMetrics{}
	}
	return a.cfg.metrics
}
