package via

import (
	"context"
	_ "embed"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/signal"
	"sync"
	"syscall"
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
	cfg     config
	mux     *http.ServeMux
	handler http.Handler
	server  *http.Server

	descs        []*cmpDescriptor
	descsMu      sync.RWMutex
	routes       map[string]string // method-and-pattern → registrar tag
	routesMu     sync.Mutex

	// appSignals holds plugin-registered, app-wide initial signal values.
	// They are injected into <meta data-signals> on every page render but
	// don't have a server-side reactive handle — clients drive them.
	appSignals   map[string]any
	appSignalsMu sync.RWMutex

	// appStore backs scope.App[T] with shared storage across every
	// session and tab. Keyed by the handle's wire key.
	appStore sync.Map

	contextRegistry      map[string]*Ctx
	contextRegistryMutex sync.RWMutex

	sessions   map[string]*session
	sessionsMu sync.RWMutex

	stopSweep     chan struct{}
	stopSweepOnce sync.Once

	middleware []Middleware

	documentHeadIncludes []h.H
	documentFootIncludes []h.H
	documentHTMLAttrs    []h.H
}

// ServeHTTP makes *App an http.Handler.
func (a *App) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.handler.ServeHTTP(w, r)
}

// AppendToHead adds nodes to the <head> of every rendered page.
func (a *App) AppendToHead(elements ...h.H) {
	for _, el := range elements {
		if el != nil {
			a.documentHeadIncludes = append(a.documentHeadIncludes, el)
		}
	}
}

// AppendToFoot adds nodes to the end of <body> on every rendered page.
func (a *App) AppendToFoot(elements ...h.H) {
	for _, el := range elements {
		if el != nil {
			a.documentFootIncludes = append(a.documentFootIncludes, el)
		}
	}
}

// AppendAttrToHTML adds attributes to the <html> element of every page.
func (a *App) AppendAttrToHTML(attrs ...h.H) {
	for _, attr := range attrs {
		if attr != nil {
			a.documentHTMLAttrs = append(a.documentHTMLAttrs, attr)
		}
	}
}

// Use installs middleware that wraps every via-served request.
func (a *App) Use(mw ...Middleware) { a.middleware = append(a.middleware, mw...) }

// RegisterAppSignal sets the initial value of a named, app-wide signal.
// Used by plugins to seed data-signals entries that the client owns
// (e.g. picocss's "_picoTheme"). The value is JSON-encoded into every
// page's <meta data-signals> on render.
func (a *App) RegisterAppSignal(key string, value any) {
	a.appSignalsMu.Lock()
	defer a.appSignalsMu.Unlock()
	if a.appSignals == nil {
		a.appSignals = map[string]any{}
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

func (a *App) registerCtx(id string, ctx *Ctx) {
	a.contextRegistryMutex.Lock()
	defer a.contextRegistryMutex.Unlock()
	a.contextRegistry[id] = ctx
}

func (a *App) unregisterCtx(id string) {
	a.contextRegistryMutex.Lock()
	defer a.contextRegistryMutex.Unlock()
	delete(a.contextRegistry, id)
}

func (a *App) getCtx(id string) (*Ctx, error) {
	a.contextRegistryMutex.RLock()
	defer a.contextRegistryMutex.RUnlock()
	if ctx, ok := a.contextRegistry[id]; ok {
		return ctx, nil
	}
	return nil, fmt.Errorf("ctx %q not found", id)
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
		logger.Log(level, msg, "via_tab", ctx.id)
	} else {
		logger.Log(level, msg)
	}
}

func (a *App) logErr(ctx *Ctx, format string, args ...any)   { a.emit(LogError, ctx, format, args...) }
func (a *App) logWarn(ctx *Ctx, format string, args ...any)  { a.emit(LogWarn, ctx, format, args...) }
func (a *App) logInfo(ctx *Ctx, format string, args ...any)  { a.emit(LogInfo, ctx, format, args...) }
func (a *App) logDebug(ctx *Ctx, format string, args ...any) { a.emit(LogDebug, ctx, format, args...) }

// Start binds and serves on the configured address. SIGINT/SIGTERM trigger
// a graceful Shutdown.
func (a *App) Start() {
	a.server = &http.Server{
		Addr:              a.cfg.addr,
		Handler:           a.handler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}
	if a.cfg.httpServerHook != nil {
		a.cfg.httpServerHook(a.server)
	}
	a.logInfo(nil, "via started at [%s]", a.cfg.addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), a.cfg.shutdownTimeout)
		defer cancel()
		if err := a.Shutdown(ctx); err != nil {
			a.logErr(nil, "shutdown error: %v", err)
		}
	}()

	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic(fmt.Sprintf("via: %v", err))
	}
}

// Shutdown disposes all live tabs and closes the server.
func (a *App) Shutdown(ctx context.Context) error {
	a.contextRegistryMutex.Lock()
	ctxs := make([]*Ctx, 0, len(a.contextRegistry))
	for _, c := range a.contextRegistry {
		ctxs = append(ctxs, c)
	}
	a.contextRegistry = make(map[string]*Ctx)
	a.contextRegistryMutex.Unlock()

	for _, c := range ctxs {
		a.disposeCtx(c)
	}

	a.stopSweepOnce.Do(func() {
		if a.stopSweep != nil {
			close(a.stopSweep)
		}
	})

	a.sessionsMu.Lock()
	a.sessions = make(map[string]*session)
	a.sessionsMu.Unlock()

	if a.server != nil {
		return a.server.Shutdown(ctx)
	}
	return nil
}

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

	a.handler = a.withSession()

	if a.cfg.sessionTTL > 0 || a.cfg.contextTTL > 0 {
		a.stopSweep = make(chan struct{})
		if a.cfg.sessionTTL > 0 {
			go a.sweepExpiredSessions()
		}
		if a.cfg.contextTTL > 0 {
			go a.sweepExpiredContexts()
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
		applyMiddleware(a.middleware, a.mux).ServeHTTP(w, r)
	})
}
