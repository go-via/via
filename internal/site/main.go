// Command site serves go-via.dev: via's documentation, written with via, so
// every demo on it is the library actually running.
package main

import (
	"cmp"
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/site"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app, mux := site.New(site.Options{Version: version, Origin: os.Getenv("VIA_ORIGIN")})
	demo.Reset(ctx, 15*time.Minute, demos.ResetAll)

	srv := &http.Server{
		Addr:              cmp.Or(os.Getenv("VIA_ADDR"), ":8080"),
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		IdleTimeout:       120 * time.Second,
		// WriteTimeout stays 0: it bounds the whole response, which would cut
		// every SSE stream at the deadline.
	}
	serve := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if errors.Is(err, http.ErrServerClosed) {
			err = nil
		}
		serve <- err
	}()

	select {
	case err := <-serve:
		return err
	case <-ctx.Done():
	}
	// A second signal during the drain must terminate, not queue behind a
	// channel nobody reads any more.
	stop()

	// Close first: Shutdown does not cancel the router's own context, so an
	// open SSE response would hold it until its deadline. Nothing races the
	// gap — Close keeps serving plain pages and refuses a late stream connect
	// with 503 (see Router.Close), and Shutdown then drains the rest.
	app.Close()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shut); err != nil {
		return err
	}
	return <-serve
}
