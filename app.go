package via

import (
	"context"
	_ "embed"
	"fmt"
	"io/fs"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-via/via/h"
)

//go:embed datastar.js
var datastarJS []byte

// App is the root of a via web app. It implements http.Handler so it can be
// passed straight to http.ListenAndServe or composed inside any std mux:
//
//	app := via.New()
//	via.Mount[Counter](app, "/counter")
//	http.ListenAndServe(":3000", app)
//
//	// or, embed under a parent mux:
//	parent := http.NewServeMux()
//	parent.Handle("/", app)
type App struct {
	cfg                 config
	mux                 *http.ServeMux
	handler             http.Handler
	server              *http.Server
	cachedChain         atomic.Pointer[http.HandlerFunc] // applyMiddleware(a.middleware, a.mux), rebuilt on Use
	cachedNotFoundChain atomic.Pointer[http.HandlerFunc] // applyMiddleware(a.middleware, a.cfg.notFoundHandler), nil if no custom 404

	descs    []*cmpDescriptor
	descsMu  sync.RWMutex
	routes   map[string]string // method-and-pattern → registrar tag
	routesMu sync.Mutex
	serverMu sync.Mutex // guards a.server while Start binds and Shutdown reads

	// appSignals holds plugin-registered, app-wide initial signal values.
	// They are injected into <meta data-signals> on every page render but
	// don't have a server-side reactive handle — clients drive them.
	appSignals   map[string]any
	appSignalsMu sync.RWMutex

	// valStates holds the L1 cache + decode closure for each value-shaped
	// StateApp key. The backplane Store cell `val:<key>` is the source of
	// truth; valCell.l1 is a per-pod cache reconciled to it. Populated at the
	// first bindApp for a key.
	valStates     map[string]*valCell
	valStatesMu   sync.Mutex
	valTailerOnce sync.Once // starts the one changes-feed tailer per App

	// sessDecoders holds the typed (Store bytes → T) decoder for each
	// StateSess wire key, shared across every session of that field — the
	// type-erased session reconcile/tailer recovers T through it.
	sessDecoders   map[string]func([]byte) (any, error)
	sessDecodersMu sync.Mutex

	// backplane backs StateAppEvents and (later) clustered StateApp/StateSess.
	// Resolved at New: a nil config backplane becomes InMemory(), so the
	// runtime always drives one Backplane code path. Drained on Shutdown.
	backplane Backplane

	// logs holds the per-(pod,key) projector state for each StateAppEvents key.
	// Lazily populated at the first bindApp for a key. Keyed by wire key.
	logs   map[string]*logState
	logsMu sync.Mutex

	// consumers holds the per-(name,key) side-effect consumer state (OnEvent),
	// started once per consumer. consumersByKey indexes them by wire key so the
	// Compactor floor can clamp below the slowest consumer's committed offset.
	consumers      map[string]*consumerState
	consumersByKey map[string][]*consumerState
	consumersMu    sync.Mutex

	contextRegistry   map[string]*Ctx
	contextRegistryMu sync.RWMutex

	sessions   map[string]*session
	sessionsMu sync.RWMutex

	stopSweep     chan struct{}
	stopSweepOnce sync.Once

	// backplaneDone is closed at the START of Shutdown, BEFORE backplane.Close,
	// so the projector/consumer tailers can tell a graceful stop (exit) from a
	// transient mid-stream disconnect (re-subscribe from the cursor and
	// rehydrate). Without it a single dropped subscription would strand a key
	// forever — the deploy-freeze class of bug the backplane exists to fix.
	backplaneDone     chan struct{}
	backplaneDoneOnce sync.Once

	// draining flips true at the start of Shutdown so /readyz reports
	// not-ready and the orchestrator stops sending new traffic while the pod
	// finishes its graceful drain.
	draining atomic.Bool

	// backplaneCtx is the parent of every backplane I/O call (Subscribe, Append,
	// CAS, LoadSnapshot, Compact). Shutdown cancels it so a wedged backend cannot
	// keep an in-flight call alive and block the drain — without it those calls
	// rode context.Background() and could never be aborted.
	backplaneCtx    context.Context
	backplaneCancel context.CancelFunc

	middlewareMu sync.Mutex
	middleware   []Middleware

	documentHeadIncludes []h.H
	documentFootIncludes []h.H
	documentHTMLAttrs    []h.H
}

// ServeHTTP makes *App an http.Handler.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if a.serveHealth(w, r) {
		return
	}
	a.handler.ServeHTTP(w, r)
}

// serveHealth answers the default liveness/readiness/health probes before the
// session + middleware chain, so a frequent k8s probe never mints a session or
// emits an access-log line. /livez and /healthz report the process is up;
// /readyz reports 503 once Shutdown has begun draining so traffic drains away
// from a departing pod while liveness stays green. Returns true if it handled
// the request. Disabled by WithoutHealthEndpoints.
func (a *App) serveHealth(w http.ResponseWriter, r *http.Request) bool {
	if a.cfg.noHealth || r.Method != http.MethodGet {
		return false
	}
	switch r.URL.Path {
	case "/livez", "/healthz":
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	case "/readyz":
		if a.draining.Load() {
			http.Error(w, "draining", http.StatusServiceUnavailable)
			return true
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	default:
		return false
	}
	return true
}

// Use installs middleware that wraps every via-served request.
//
// Boot-only: panics if called after Start has bound the server.
// Concurrent Use calls are safe — the middleware slice and the chain
// rebuild are serialized under one mutex.
func (a *App) Use(mw ...Middleware) {
	a.serverMu.Lock()
	started := a.server != nil
	a.serverMu.Unlock()
	if started {
		panic("via: App.Use called after Start; install middleware during boot")
	}
	a.middlewareMu.Lock()
	a.middleware = append(a.middleware, mw...)
	chain := applyMiddleware(a.middleware, a.mux)
	a.middlewareMu.Unlock()
	hf := http.HandlerFunc(chain.ServeHTTP)
	a.cachedChain.Store(&hf)
}

// rebuildChain caches the post-middleware http.Handler used by every
// request. Without this cache we'd rebuild the closure chain in
// withSession on every request — N+1 allocations per hit, where N is
// the number of installed middlewares.
//
// We wrap the result as *http.HandlerFunc so the atomic.Pointer stays
// statically typed and the load site can deref-and-call without a
// runtime type assertion.
func (a *App) rebuildChain() {
	chain := applyMiddleware(a.middleware, a.mux)
	hf := http.HandlerFunc(chain.ServeHTTP)
	a.cachedChain.Store(&hf)
	if a.cfg.notFoundHandler != nil {
		// Wrap the custom 404 handler with the same middleware chain
		// as matched routes so CSP / RequestID / Recover / AccessLog
		// still apply on the not-found path. Without this the user's
		// 404 page renders without any of the cross-cutting behavior
		// they configured for the rest of the app.
		nf := applyMiddleware(a.middleware, a.cfg.notFoundHandler)
		nfHf := http.HandlerFunc(nf.ServeHTTP)
		a.cachedNotFoundChain.Store(&nfHf)
	}
}

// RegisterAppSignal sets the initial value of a named, app-wide signal.
// Used by plugins to seed data-signals entries that the client owns
// (e.g. picocss's "_picoTheme"). The value is JSON-encoded into every
// page's <meta data-signals> on render.
func (a *App) RegisterAppSignal(key string, value any) {
	a.appSignalsMu.Lock()
	defer a.appSignalsMu.Unlock()
	if _, dup := a.appSignals[key]; dup {
		panic(fmt.Sprintf("via.RegisterAppSignal: duplicate app signal key %q — "+
			"two plugins (or a plugin and user code) registered the same key; rename one", key))
	}
	a.appSignals[key] = value
}

// HandleFunc registers a non-via handler on the app's mux.
func (a *App) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	a.claimRoute(pattern, "HandleFunc")
	a.mux.HandleFunc(pattern, handler)
}

// Handle registers a non-via http.Handler on the app's mux.
func (a *App) Handle(pattern string, handler http.Handler) {
	a.claimRoute(pattern, "Handle")
	a.mux.Handle(pattern, handler)
}

// HandleStatic serves files under prefix from fsys. Common pattern for
// shipping a single binary with embedded assets:
//
//	//go:embed static
//	var assets embed.FS
//	sub, _ := fs.Sub(assets, "static")
//	app.HandleStatic("/assets/", sub)
//
// The pattern ends with a trailing slash; the prefix is stripped before
// the file lookup. The handler claims `GET <prefix>` so the route table
// reflects the registration.
func (a *App) HandleStatic(prefix string, fsys fs.FS) {
	pattern := "GET " + prefix
	a.claimRoute(pattern, "HandleStatic")
	a.mux.Handle(prefix,
		http.StripPrefix(prefix, http.FileServer(http.FS(fsys))))
}

// claimRoute records that pattern has been claimed by tag and panics if the
// same pattern is registered twice. Catching the conflict early surfaces
// silent footguns ("why does only the second Mount win?") at boot rather
// than at the next request.
func (a *App) claimRoute(pattern, tag string) {
	a.routesMu.Lock()
	defer a.routesMu.Unlock()
	if prev, ok := a.routes[pattern]; ok {
		panic(fmt.Sprintf(
			"via: route %q already registered (by %s); now %s would overwrite it",
			pattern, prev, tag))
	}
	a.routes[pattern] = tag
}

// mountDescriptor implements Mountable for *App: route is taken as-is.
func (a *App) mountDescriptor(d *cmpDescriptor, route string) {
	d.route = route
	checkPathParams(d, route)
	a.registerDescriptor(d)
}

func (a *App) registerDescriptor(d *cmpDescriptor) {
	a.descsMu.Lock()
	a.descs = append(a.descs, d)
	a.descsMu.Unlock()
	pattern := "GET " + d.route
	a.claimRoute(pattern, "Mount["+d.typ.Name()+"]")
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.renderPage(d, w, r)
	})
	guarded := applyMiddleware(d.groupMW, final)
	// Plant the logical route before group middleware runs so a guard reads it
	// via RouteFrom — matching the action/SSE entry points where r.URL.Path is
	// the shared /_action or /_sse rather than this page's route.
	route := d.route
	a.mux.Handle(pattern, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		guarded.ServeHTTP(w, requestWithRoute(r, route))
	}))
}

// tryRegisterCtx enforces the maxContexts cap atomically with the
// registry write. Returns false if the cap is set and already met —
// the caller must respond with 503 instead of registering. Separate
// "live count" check + register opens a TOCTOU race under heavy
// concurrent page loads; this fuses both steps under a single Lock.
func (a *App) tryRegisterCtx(ctx *Ctx, limit int) bool {
	a.contextRegistryMu.Lock()
	if limit > 0 && len(a.contextRegistry) >= limit {
		a.contextRegistryMu.Unlock()
		return false
	}
	a.contextRegistry[ctx.id] = ctx
	live := len(a.contextRegistry)
	a.contextRegistryMu.Unlock()
	a.metricsOrNoop().Gauge("via.ctx.live", float64(live))
	return true
}

func (a *App) unregisterCtx(id string) {
	a.contextRegistryMu.Lock()
	delete(a.contextRegistry, id)
	live := len(a.contextRegistry)
	a.contextRegistryMu.Unlock()
	a.metricsOrNoop().Gauge("via.ctx.live", float64(live))
}

// getCtx returns the live Ctx for id and ok=true; ok=false if the id is
// unknown (a cleaned-up tab, a forged via_tab, or a stale reconnect after
// disposal). Comma-ok shape so callers don't allocate an error wrapper
// just to throw it away — every caller maps a miss to a 404 directly.
func (a *App) getCtx(id string) (*Ctx, bool) {
	a.contextRegistryMu.RLock()
	defer a.contextRegistryMu.RUnlock()
	ctx, ok := a.contextRegistry[id]
	return ctx, ok
}

func (a *App) emit(level LogLevel, ctx *Ctx, format string, args ...any) {
	if level < a.cfg.logLevel {
		return
	}
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}
	logger := a.cfg.logger
	if logger == nil {
		logger = defaultLogger{}
	}
	if ctx != nil {
		logger.Log(level, msg, tabSignalKey, ctx.id)
	} else {
		logger.Log(level, msg)
	}
}

func (a *App) logErr(ctx *Ctx, format string, args ...any)  { a.emit(LogError, ctx, format, args...) }
func (a *App) logWarn(ctx *Ctx, format string, args ...any) { a.emit(LogWarn, ctx, format, args...) }
func (a *App) logInfo(ctx *Ctx, format string, args ...any) { a.emit(LogInfo, ctx, format, args...) }

// Logger returns the [Logger] configured on a — either the user's
// WithLogger, or the default log.Printf-backed implementation when
// none was set. Records emitted below the App's configured log level
// (see [WithLogLevel]) are dropped, matching the behaviour the via
// runtime applies to its own warnings.
//
// Used by middleware in via/mw to emit access logs and panic reports
// through the same pipe as the runtime's own warnings.
func (a *App) Logger() Logger {
	if a == nil {
		return defaultLogger{}
	}
	base := a.cfg.logger
	if base == nil {
		base = defaultLogger{}
	}
	minLevel := a.cfg.logLevel
	return LoggerFunc(func(level LogLevel, msg string, kv ...any) {
		if level < minLevel {
			return
		}
		base.Log(level, msg, kv...)
	})
}

// New constructs an *App with the given options.
func New(opts ...Option) *App {
	// MethodName parses the Go runtime's "-fm" trampoline naming —
	// undocumented internal. Verify it still produces the expected
	// shape so a Go toolchain upgrade that changes the format trips
	// at startup, not at the first action POST six hours later.
	verifyMethodNameTrampoline()

	mux := http.NewServeMux()
	backplaneCtx, backplaneCancel := context.WithCancel(context.Background())
	a := &App{
		mux:             mux,
		contextRegistry: make(map[string]*Ctx),
		sessions:        make(map[string]*session),
		appSignals:      make(map[string]any),
		routes:          make(map[string]string),
		logs:            make(map[string]*logState),
		valStates:       make(map[string]*valCell),
		sessDecoders:    make(map[string]func([]byte) (any, error)),
		backplaneDone:   make(chan struct{}),
		backplaneCtx:    backplaneCtx,
		backplaneCancel: backplaneCancel,
		cfg: config{
			addr:              ":3000",
			logLevel:          LogWarn,
			title:             "Via",
			shutdownTimeout:   5 * time.Second,
			sessionTTL:        30 * time.Minute,
			contextTTL:        15 * time.Minute,
			reconcileInterval: 5 * time.Second,
			snapshotInterval:  64,
			sseHeartbeat:      25 * time.Second,
			sseWriteTimeout:   10 * time.Second,
			maxRequestBody:    1 << 20,
			maxUploadSize:     32 << 20,
			// Secure-by-default: the deployment surface (internal tools,
			// admin dashboards) is exactly where a non-Secure cookie leaks
			// on an http downgrade. WithInsecureCookies opts out for dev.
			secureCookies: true,
		},
	}
	for _, opt := range opts {
		opt(&a.cfg)
	}
	a.cfg.validate()
	for _, plugin := range a.cfg.plugins {
		if plugin != nil {
			plugin.Register(a)
		}
	}

	// A nil backplane resolves to the in-process default, so the Backplane
	// interface is exercised on every single-pod run (no nil-special-case path).
	a.backplane = a.cfg.backplane
	if a.backplane == nil {
		a.backplane = InMemory()
	}

	// Clustered only: tail the shared broadcast feed so site-wide Broadcasts
	// issued on any pod reach this pod's tabs. The default in-process backplane
	// has no peers, so a single-pod app applies broadcasts inline instead.
	if a.cfg.backplane != nil {
		a.startBroadcastTailer()
	}

	a.mux.HandleFunc("GET /_datastar.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write(datastarJS)
	})
	a.mux.HandleFunc("GET /_sse", a.handleSSE)
	a.mux.HandleFunc("POST /_action/{id}", a.handleAction)
	a.mux.HandleFunc("POST /_sse/close", a.handleSSEClose)

	a.rebuildChain()
	a.handler = a.withSession()

	// The context-TTL sweep only reaps stream-less ctxs: a connected stream
	// is kept alive by Ctx.connected regardless of the TTL, so a short TTL
	// can no longer kill a live tab and needs no guard against the heartbeat.
	if a.cfg.sessionTTL > 0 || a.cfg.contextTTL > 0 || a.cfg.reconcileInterval > 0 {
		a.stopSweep = make(chan struct{})
		if a.cfg.sessionTTL > 0 {
			go a.runSweep(a.cfg.sessionTTL/2, time.Millisecond, a.removeExpiredSessions)
		}
		if a.cfg.contextTTL > 0 {
			go a.runSweep(a.cfg.contextTTL/2, time.Second, a.removeExpiredContexts)
		}
		if a.cfg.reconcileInterval > 0 {
			go a.runSweep(a.cfg.reconcileInterval, a.cfg.reconcileInterval, a.reconcileValues)
		}
	}

	return a
}

func (a *App) withSession() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// mux.Handler resolves the route without serving — used both
		// to gate session creation (unmatched paths get no session,
		// bounding memory under crawler / 404 floods) and to pick the
		// right chain (custom 404 if configured).
		_, pattern := a.mux.Handler(r)
		matched := pattern != ""

		if matched {
			if a.getOrCreateSession(w, r) == nil {
				a.logWarn(nil, "max sessions reached (%d); rejecting request", a.cfg.maxSessions)
				http.Error(w, "server is at capacity", http.StatusServiceUnavailable)
				return
			}
		}
		// Stamp the app pointer into r so middleware can resolve the
		// session via via.RequestSession(r) (used by via/sess.Get on
		// the *http.Request branch) without holding a *Ctx yet.
		r = r.WithContext(context.WithValue(r.Context(), appKey{}, a))

		if !matched {
			if nf := a.cachedNotFoundChain.Load(); nf != nil {
				(*nf).ServeHTTP(w, r)
				return
			}
		}
		(*a.cachedChain.Load()).ServeHTTP(w, r)
	})
}
