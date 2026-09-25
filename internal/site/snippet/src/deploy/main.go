// Package deploy holds the compile-checked production wiring the /deploy page
// shows.
package deploy

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

type Home struct{}

func (p *Home) View() h.H { return h.P(h.Str("hello")) }

// drainDelay is how long /readyz fails before the router closes. Set it to
// what your balancer needs to mark the pod down: probe interval times the
// failure threshold.
const drainDelay = 5 * time.Second

func Run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// With a driver imported, e.g. _ "github.com/jackc/pgx/v5/stdlib".
	db, err := sql.Open("pgx", os.Getenv("DATABASE_URL"))
	if err != nil {
		return err
	}
	defer db.Close()

	// snippet:start router
	origin := os.Getenv("VIA_ORIGIN")
	if origin == "" {
		return errors.New("VIA_ORIGIN unset")
	}
	// No WithSessionKey: via reads VIA_SESSION_KEY from the environment.
	r := via.NewRouter(
		via.WithTrustedOrigin(origin),
		via.WithSecureCookies(),
		via.WithSessionStore(SQLSessions{DB: db}),
		via.WithLogger(slog.New(slog.NewJSONHandler(os.Stderr, nil))),
		via.WithMaxSSEConn(5000),
	)
	via.Mount(r, "/", Home{})
	// snippet:end

	// snippet:start health
	var ready atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, _ *http.Request) {
		if !ready.Load() {
			http.Error(w, "draining", http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, "ok\n")
	})
	mux.Handle("/", r)
	// snippet:end

	// snippet:start server
	srv := &http.Server{
		Addr:              os.Getenv("VIA_ADDR"),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout stays 0: it bounds the whole response, and a stream is
		// one response for the life of the tab.
	}
	// snippet:end

	// snippet:start shutdown
	serve := make(chan error, 1)
	go func() { serve <- srv.ListenAndServe() }()
	ready.Store(true)

	select {
	case err := <-serve:
		return err
	case <-ctx.Done():
	}
	stop()

	ready.Store(false)
	time.Sleep(drainDelay)
	r.Close()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return srv.Shutdown(shut)
	// snippet:end
}
