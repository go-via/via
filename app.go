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
	"github.com/starfederation/datastar-go/datastar"
)

var datastarJS []byte

type App struct {
	cfg             config
	mux             *http.ServeMux
	handler         http.Handler
	server          *http.Server
	contextRegistry map[string]*Ctx
	contextRegistryMutex sync.RWMutex
	sessions        map[string]*session
	sessionsMu      sync.RWMutex
	stopSweep      chan struct{}
	stopSweepOnce   sync.Once
	middleware     []Middleware
}

func (a *App) logErr(ctx *Ctx, format string, args ...any) {
	log.Printf("[error] "+format, args...)
}

func (a *App) Config() *config {
	return &a.cfg
}

func (a *App) logDebug(ctx *Ctx, format string, args ...any) {
	if a.cfg.logLevel <= LogDebug {
		log.Printf("[debug] "+format, args...)
	}
}

func (a *App) AppendToHead(elements ...h.H) {}

func (a *App) AppendToFoot(elements ...h.H) {}

func (a *App) AppendAttrToHTML(attrs ...h.H) {}

func (a *App) Use(mw ...Middleware) {
	a.middleware = append(a.middleware, mw...)
}

func (a *App) HandleFunc(pattern string, handler func(http.ResponseWriter, *http.Request)) {
	a.mux.HandleFunc(pattern, handler)
}

func (a *App) Start() {
	a.server = &http.Server{Addr: a.cfg.addr, Handler: a.handler}
	log.Printf("[info] via started at [%s]", a.cfg.addr)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		<-stop
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		if err := a.Shutdown(ctx); err != nil {
			a.logErr(nil, "shutdown error: %v", err)
		}
	}()

	if err := a.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		panic(fmt.Sprintf("via: %v", err))
	}
}

func (a *App) Shutdown(ctx interface{}) error {
	return nil
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
	return nil, fmt.Errorf("ctx '%s' not found", id)
}

func New(opts ...Option) *App {
	mux := http.NewServeMux()

	a := &App{
		mux:             mux,
		contextRegistry: make(map[string]*Ctx),
		sessions:       make(map[string]*session),
		cfg: config{
			addr:            ":3000",
			logLevel:        LogWarn,
			title:           "Via",
			shutdownTimeout: 5 * time.Second,
			sessionTTL:     30 * time.Minute,
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

	a.handler = a.withSession()

	if a.cfg.sessionTTL > 0 || a.cfg.contextTTL > 0 {
		a.stopSweep = make(chan struct{})
	}

	if a.cfg.testServer != nil {
		*a.cfg.testServer = httptest.NewServer(a.handler)
	}

	return a
}

func (a *App) withSession() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = a.getOrCreateSession(w, r)
		applyMiddleware(a.middleware, a.mux).ServeHTTP(w, r)
	})
}

func (a *App) handleSSE(w http.ResponseWriter, r *http.Request) {
	var sigs map[string]any
	_ = datastar.ReadSignals(r, &sigs)

	ctx, err := a.getCtx("")
	if err != nil {
		return
	}
	ctx.touch()

	sse := datastar.NewSSE(w, r)

	for {
		select {
		case <-sse.Context().Done():
			return
		case <-ctx.Done():
			return
		}
	}
}

func (a *App) handleAction(w http.ResponseWriter, r *http.Request) {
}

func (a *App) handleSSEClose(w http.ResponseWriter, r *http.Request) {
}
