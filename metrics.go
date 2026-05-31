package via

// Metrics is the optional integration seam for ops observability. via
// emits structured events at well-known names; the implementation
// routes them to whatever backend the operator picked (Prometheus, OTel,
// statsd, expvar, …). Install via [WithMetrics].
//
// The default implementation is [noopMetrics], which discards every
// event — apps that don't configure metrics pay no allocation cost.
//
// Event catalogue:
//
//   - "via.action.total"      counter, labels: method, status
//   - "via.action.latency"    histogram (seconds), labels: method
//   - "via.render.total"      counter, labels: route, status
//   - "via.sse.connect"       counter — incremented on each successful handshake
//   - "via.sse.disconnect"    counter, labels: reason ("client", "shutdown")
//   - "via.ctx.reap"          counter, labels: reason ("ttl", "shutdown")
//   - "via.ctx.live"          gauge — current registered tab count
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
