package via

import (
	"context"
	_ "embed"
	"fmt"
	"log"
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

// App is the root application.
type App struct {
	cfg                  config
	mux                  *http.ServeMux
	handler              http.Handler
	server               *http.Server
	contextRegistry      map[string]*Ctx
	contextRegistryMutex sync.RWMutex
	sessions             map[string]*session
	sessionsMu           sync.RWMutex
	stopSweep            chan struct{}
	stopSweepOnce        sync.Once
	middleware           []Middleware
	layoutFn func(cmp *Cmp)
	documentHeadIncludes []h.H
	documentFootIncludes []h.H
	documentHTMLAttrs    []h.H
	signals              map[string]any
}

func (a *App) logPanic(format string, args ...any) {
	log.Printf("[fatal] msg=%q", fmt.Sprintf(format, args...))
}

func (a *App) logErr(ctx *Ctx, format string, args ...any) {
	cRef := ""
	if ctx != nil && ctx.cmp != nil {
		cRef = fmt.Sprintf("via_tab=%q ", ctx.cmp.route)
	}
	log.Printf("[error] %smsg=%q", cRef, fmt.Sprintf(format, args...))
}

func (a *App) logWarn(ctx *Ctx, format string, args ...any) {
	if a.cfg.logLevel <= LogWarn {
		cRef := ""
		if ctx != nil && ctx.cmp != nil {
			cRef = fmt.Sprintf("via_tab=%q ", ctx.cmp.route)
		}
		log.Printf("[warn] %smsg=%q", cRef, fmt.Sprintf(format, args...))
	}
}

func (a *App) logInfo(ctx *Ctx, format string, args ...any) {
	if a.cfg.logLevel <= LogInfo {
		cRef := ""
		if ctx != nil && ctx.cmp != nil {
			cRef = fmt.Sprintf("via_tab=%q ", ctx.cmp.route)
		}
		log.Printf("[info] %smsg=%q", cRef, fmt.Sprintf(format, args...))
	}
}

func (a *App) logDebug(ctx *Ctx, format string, args ...any) {
	if a.cfg.logLevel <= LogDebug {
		cRef := ""
		if ctx != nil && ctx.cmp != nil {
			cRef = fmt.Sprintf("via_tab=%q ", ctx.cmp.route)
		}
		log.Printf("[debug] %smsg=%q", cRef, fmt.Sprintf(format, args...))
	}
}

// AppendToHead appends the given h.H nodes to the head of the base HTML document.
func (a *App) AppendToHead(elements ...h.H) {
	for _, el := range elements {
		if el != nil {
			a.documentHeadIncludes = append(a.documentHeadIncludes, el)
		}
	}
}

// AppendAttrToHTML appends attributes to the <html> element of every page.
func (a *App) AppendAttrToHTML(attrs ...h.H) {
	for _, attr := range attrs {
		if attr != nil {
			a.documentHTMLAttrs = append(a.documentHTMLAttrs, attr)
		}
	}
}

// AppendToFoot appends the given h.H nodes to the end of the base HTML document body.
func (a *App) AppendToFoot(elements ...h.H) {
	for _, el := range elements {
		if el != nil {
			a.documentFootIncludes = append(a.documentFootIncludes, el)
		}
	}
}

func (a *App) registerCtx(id string, ctx *Ctx) {
	a.contextRegistryMutex.Lock()
	defer a.contextRegistryMutex.Unlock()
	if ctx == nil {
		a.logErr(nil, "failed to add nil context to registry")
		return
	}
	a.contextRegistry[id] = ctx
	a.logDebug(ctx, "new context added to registry")
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
	return nil, fmt.Errorf("ctx '%s' not found", id)
}

func (a *App) sweepExpiredContexts() {
	interval := a.cfg.contextTTL / 2
	if interval <= 0 {
		interval = time.Millisecond
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-a.stopSweep:
			return
		case <-ticker.C:
			a.removeExpiredContexts()
		}
	}
}

func (a *App) removeExpiredContexts() {
	cutoff := time.Now().Add(-a.cfg.contextTTL).UnixNano()
	a.contextRegistryMutex.Lock()
	expired := make([]*Ctx, 0)
	for id, ctx := range a.contextRegistry {
		if ctx.lastAccess.Load() < cutoff {
			expired = append(expired, ctx)
			delete(a.contextRegistry, id)
		}
	}
	a.contextRegistryMutex.Unlock()
	for _, ctx := range expired {
		a.disposeCtx(ctx)
	}
}

func (a *App) disposeCtx(ctx *Ctx) {
	if ctx == nil {
		return
	}
	ctx.mux.Lock()
	if ctx.disposed {
		ctx.mux.Unlock()
		return
	}
	ctx.disposed = true
	close(ctx.doneChan)
	if ctx.queue != nil {
		ctx.queue.mu.Lock()
		ctx.queue.disposed = true
		ctx.queue.mu.Unlock()
	}
	ctx.mux.Unlock()
	if ctx.cmp != nil {
		a.safeDispose(ctx, ctx.cmp.disposeFn)
		for _, comp := range ctx.cmp.components {
			a.safeDispose(ctx, comp.disposeFn)
		}
	}
}

func (a *App) safeDispose(ctx *Ctx, fn func()) {
	if fn == nil {
		return
	}
	defer func() {
		if r := recover(); r != nil {
			a.logErr(ctx, "dispose callback panicked: %v", r)
		}
	}()
	fn()
}

// Shutdown gracefully shuts down the application.
func (a *App) Shutdown(ctx context.Context) error {
	a.contextRegistryMutex.Lock()
	contexts := make([]*Ctx, 0, len(a.contextRegistry))
	for _, c := range a.contextRegistry {
		contexts = append(contexts, c)
	}
	a.contextRegistry = make(map[string]*Ctx)
	a.contextRegistryMutex.Unlock()

	for _, c := range contexts {
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

// Start starts the Via HTTP server on the configured address.
// Panics if the server cannot bind to the address.
func (a *App) Start() {
	a.server = &http.Server{Addr: a.cfg.addr, Handler: a.handler}
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

// Layout sets the default layout for all pages.
func (a *App) Layout(layoutFn func(cmp *Cmp)) {
	a.layoutFn = layoutFn
}

// HandleFunc registers an HTTP handler on the app's request multiplexer.
func (a *App) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	a.mux.HandleFunc(pattern, handler)
}

// New creates a new *App with default configuration.
func New(opts ...Option) *App {
	mux := http.NewServeMux()

	a := &App{
		mux:             mux,
		contextRegistry: make(map[string]*Ctx),
		sessions:        make(map[string]*session),
		signals:         make(map[string]any),
		cfg: config{
			addr:            ":3000",
			logLevel:        LogWarn,
			title:           "Via",
			shutdownTimeout: 5 * time.Second,
			sessionTTL:      30 * time.Minute,
			contextTTL:      15 * time.Minute,
			sseHeartbeat:    25 * time.Second,
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

	a.handler = a.withSession(a.mux)

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
