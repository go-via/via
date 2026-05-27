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

// Shutdown gracefully tears down the app:
//
//  1. Every live Ctx's Done channel is closed so SSE drain loops and
//     Stream goroutines exit promptly.
//  2. The registry is cleared so concurrent action POSTs that arrive
//     after this point 404 instead of running against a half-disposed
//     Ctx.
//  3. The underlying http.Server is drained (waits for in-flight non-SSE
//     handlers up to ctx's deadline).
//  4. Per-Ctx OnDispose runs, serialized against any in-flight action
//     via the per-Ctx action mutex.
//
// Sessions and the TTL sweeper are torn down last. The error from the
// http.Server's Shutdown is returned; a wedged OnDispose handler does
// not propagate but is logged.
func (a *App) Shutdown(ctx context.Context) error {
	a.contextRegistryMu.Lock()
	ctxs := make([]*Ctx, 0, len(a.contextRegistry))
	for _, c := range a.contextRegistry {
		ctxs = append(ctxs, c)
	}
	clear(a.contextRegistry)
	a.contextRegistryMu.Unlock()

	// Step 1: wake every long-lived loop on this Ctx (SSE drain,
	// Stream goroutines, user code watching Done) so they exit before
	// we wait for action drain.
	for _, c := range ctxs {
		a.signalDispose(c)
	}

	// Step 2: drain in-flight non-SSE handlers via the http.Server.
	a.serverMu.Lock()
	srv := a.server
	a.serverMu.Unlock()
	var srvErr error
	if srv != nil {
		srvErr = srv.Shutdown(ctx)
	}

	// Step 3: run OnDispose under actionMu. Done after srv.Shutdown so
	// handlers that were mid-action have finished their work and OnDispose
	// sees a quiescent composition.
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

	return srvErr
}
