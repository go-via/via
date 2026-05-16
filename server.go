package via

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// HTTPServer returns an *http.Server configured with the app as its
// handler and every WithReadTimeout/WithWriteTimeout/WithIdleTimeout/
// WithReadHeaderTimeout option applied. Useful when the caller wants
// to bind directly (TLS, custom listener, ALB sidecar) instead of
// going through Start. The returned server has no listener attached;
// the caller drives ListenAndServe / ListenAndServeTLS themselves.
//
// HTTPServer is also what Start uses internally — same defaults.
func (a *App) HTTPServer() *http.Server {
	srv := &http.Server{
		Addr:              a.cfg.addr,
		Handler:           a.handler,
		ReadHeaderTimeout: cmp.Or(a.cfg.readHeaderTimeout, 10*time.Second),
		ReadTimeout:       a.cfg.readTimeout,
		WriteTimeout:      a.cfg.writeTimeout,
		IdleTimeout:       cmp.Or(a.cfg.idleTimeout, 120*time.Second),
		MaxHeaderBytes:    1 << 20,
	}
	if a.cfg.httpServerHook != nil {
		a.cfg.httpServerHook(srv)
	}
	return srv
}

// Start binds and serves on the configured address. SIGINT/SIGTERM trigger
// a graceful Shutdown.
func (a *App) Start() {
	srv := a.HTTPServer()
	a.serverMu.Lock()
	a.server = srv
	a.serverMu.Unlock()
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

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		panic(fmt.Sprintf("via: %v", err))
	}
}

// Shutdown disposes all live tabs and closes the server.
func (a *App) Shutdown(ctx context.Context) error {
	a.contextRegistryMu.Lock()
	ctxs := make([]*Ctx, 0, len(a.contextRegistry))
	for _, c := range a.contextRegistry {
		ctxs = append(ctxs, c)
	}
	clear(a.contextRegistry)
	a.contextRegistryMu.Unlock()

	for _, c := range ctxs {
		a.disposeCtx(c)
	}

	a.stopSweepOnce.Do(func() {
		if a.stopSweep != nil {
			close(a.stopSweep)
		}
	})

	a.sessionsMu.Lock()
	clear(a.sessions)
	a.sessionsMu.Unlock()

	a.serverMu.Lock()
	srv := a.server
	a.serverMu.Unlock()
	if srv != nil {
		return srv.Shutdown(ctx)
	}
	return nil
}
