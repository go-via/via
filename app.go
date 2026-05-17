package via

import (
	"context"
	_ "embed"
	"fmt"
	"io/fs"
	"net/http"
	"net/http/httptest"
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
	cfg         config
	mux         *http.ServeMux
	handler     http.Handler
	server      *http.Server
	cachedChain atomic.Pointer[http.HandlerFunc] // applyMiddleware(a.middleware, a.mux), rebuilt on Use

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

	// appStore backs scope.App[T] with shared storage across every
	// session and tab. Keyed by the handle's wire key.
	appStore sync.Map

	contextRegistry   map[string]*Ctx
	contextRegistryMu sync.RWMutex

	sessions   map[string]*session
	sessionsMu sync.RWMutex

	stopSweep     chan struct{}
	stopSweepOnce sync.Once

	middlewareMu sync.Mutex
	middleware   []Middleware

	documentHeadIncludes []h.H
	documentFootIncludes []h.H
	documentHTMLAttrs    []h.H
}

// ServeHTTP makes *App an http.Handler.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.handler.ServeHTTP(w, r)
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
}

// RegisterAppSignal sets the initial value of a named, app-wide signal.
// Used by plugins to seed data-signals entries that the client owns
// (e.g. picocss's "_picoTheme"). The value is JSON-encoded into every
// page's <meta data-signals> on render.
func (a *App) RegisterAppSignal(key string, value any) {
	a.appSignalsMu.Lock()
	a.appSignals[key] = value
	a.appSignalsMu.Unlock()
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
	a.mux.Handle(pattern, applyMiddleware(d.groupMW, final))
}

func (a *App) registerCtx(ctx *Ctx) {
	a.contextRegistryMu.Lock()
	defer a.contextRegistryMu.Unlock()
	a.contextRegistry[ctx.id] = ctx
}

func (a *App) unregisterCtx(id string) {
	a.contextRegistryMu.Lock()
	defer a.contextRegistryMu.Unlock()
	delete(a.contextRegistry, id)
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

// New constructs an *App with the given options.
func New(opts ...Option) *App {
	mux := http.NewServeMux()
	a := &App{
		mux:             mux,
		contextRegistry: make(map[string]*Ctx),
		sessions:        make(map[string]*session),
		appSignals:      make(map[string]any),
		routes:          make(map[string]string),
		cfg: config{
			addr:            ":3000",
			logLevel:        LogWarn,
			title:           "Via",
			shutdownTimeout: 5 * time.Second,
			sessionTTL:      30 * time.Minute,
			contextTTL:      15 * time.Minute,
			sseHeartbeat:    25 * time.Second,
			maxRequestBody:  1 << 20,
		},
	}
	for _, opt := range opts {
		opt(&a.cfg)
	}
	for _, plugin := range a.cfg.plugins {
		if plugin != nil {
			plugin.Register(a)
		}
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

	if a.cfg.sessionTTL > 0 || a.cfg.contextTTL > 0 {
		a.stopSweep = make(chan struct{})
		if a.cfg.sessionTTL > 0 {
			go a.runSweep(a.cfg.sessionTTL/2, time.Millisecond, a.removeExpiredSessions)
		}
		if a.cfg.contextTTL > 0 {
			go a.runSweep(a.cfg.contextTTL/2, time.Second, a.removeExpiredContexts)
		}
	}

	if a.cfg.testServer != nil {
		*a.cfg.testServer = httptest.NewServer(a.handler)
	}
	return a
}

func (a *App) withSession() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = a.getOrCreateSession(w, r)
		// Stamp the app pointer into r so middleware can resolve the
		// session via via.GetSess[T](r) without holding a *Ctx yet.
		r = r.WithContext(context.WithValue(r.Context(), appKey{}, a))
		// Detour through a 404 sniffer if a custom not-found handler
		// is configured. The mux's default 404 path is opaque, so we
		// pre-check via mux.Handler — if it returns the "not found"
		// handler, we run the user's WithNotFound callback instead.
		if a.cfg.notFoundHandler != nil {
			if _, pattern := a.mux.Handler(r); pattern == "" {
				a.cfg.notFoundHandler.ServeHTTP(w, r)
				return
			}
		}
		(*a.cachedChain.Load()).ServeHTTP(w, r)
	})
}
