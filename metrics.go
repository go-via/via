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
//   - "via.sse.disconnect"    counter, labels: reason ("client", "shutdown", "ttl")
//   - "via.ctx.live"          gauge — current registered tab count
//
// Labels are passed as flat key,value pairs to keep the call site
// allocation-free in the noop path.
type Metrics interface {
	Counter(name string, labels ...string)
	Gauge(name string, value float64, labels ...string)
	Histogram(name string, value float64, labels ...string)
}

// Disconnect reasons reported on the "reason" label of the
// "via.sse.disconnect" counter (see the event catalogue above).
const (
	// disconnectClient covers the client going away on its own — the
	// request context cancelling, a failed heartbeat/patch write, or an
	// explicit tab-close beacon.
	disconnectClient = "client"
	// disconnectShutdown covers App.Shutdown disposing the ctx.
	disconnectShutdown = "shutdown"
	// disconnectTTL covers the idle-TTL sweep evicting the ctx.
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
