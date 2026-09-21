// Command site serves go-via.dev: via's documentation, written with via, so
// every demo on it is the library actually running.
package main

import (
	"cmp"
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"errors"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/content"
	"go-via.dev/site/demo"
	"go-via.dev/site/demos"
	"go-via.dev/site/shell"
)

//go:embed static
var staticFS embed.FS

var version = "dev"

func errorPage(ctx *via.Ctx, e via.PageError) h.H {
	title, detail := "Something broke", e.Detail
	switch e.Reason {
	case via.ReasonNotFound:
		title, detail = "Not found", "That URL is not part of the docs."
	case via.ReasonForbidden, via.ReasonMethodNotAllowed, via.ReasonGone:
		title = "Request refused"
	case via.ReasonTooLarge:
		title = "Request too large"
	}
	return shell.Page(shell.NavItem{Title: title},
		h.P(h.Str(detail)),
		h.P(h.A(h.Href("/"), h.Str("Back to the front page"))),
	)
}

func routerOptions() []via.Option {
	opts := []via.Option{
		via.WithHead(via.Head{
			Lang: "en",
			// Raw is emitted unparsed, so WithHead panics on a script or
			// style in it: those belong in Assets, where the CSP sees them.
			Raw: `<meta name="viewport" content="width=device-width, initial-scale=1">` +
				`<link rel="icon" type="image/svg+xml" href="/static/brand/icon-amber-ink.svg">`,
			Assets: via.Assets{
				Styles:  []via.Style{{Href: "/static/site.css"}, {Href: "/static/chroma.css"}},
				Scripts: []via.Script{{Src: "/static/inspector.js", Defer: true}},
				Preload: []via.Preload{
					{Href: "/static/fonts/inter.woff2", As: "font"},
					{Href: "/static/fonts/jetbrains-mono-400.woff2", As: "font"},
				},
			},
		}),
		via.WithErrorPage(errorPage),
	}
	if o := os.Getenv("VIA_ORIGIN"); o != "" {
		opts = append(opts, via.WithTrustedOrigin(o), via.WithSecureCookies())
	} else {
		log.Print("VIA_ORIGIN unset: every origin is accepted and cookies are not Secure")
	}
	return opts
}

func newApp() *via.Router {
	app := via.NewRouter(routerOptions()...)

	via.Mount(app, "/", content.Landing{})
	via.Mount(app, "/actions", content.NewActions())
	via.Mount(app, "/signals", content.Signals{})
	via.Mount(app, "/live", content.NewLive())
	via.Mount(app, "/islands", content.Islands{})
	via.Mount(app, "/platform", content.Platform{})
	via.Mount(app, "/reference", content.Reference{})

	return app
}

func newMux(app http.Handler) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/static/", staticHandler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("ok " + version + "\n"))
	})
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Write([]byte("User-agent: *\nAllow: /\n"))
	})
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/brand/icon-amber-ink.svg", http.StatusMovedPermanently)
	})
	mux.Handle("/", app)
	return mux
}

var staticETags = staticDigests()

func staticDigests() map[string]string {
	digests := map[string]string{}
	err := fs.WalkDir(staticFS, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := staticFS.ReadFile(p)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(b)
		digests["/"+p] = `"` + hex.EncodeToString(sum[:]) + `"`
		return nil
	})
	if err != nil {
		panic(err)
	}
	return digests
}

var immutable = []string{"/static/vendor/", "/static/fonts/", "/static/data/", "/static/brand/"}

func staticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	files := http.StripPrefix("/static/", http.FileServerFS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		etag, ok := staticETags[r.URL.Path]
		if !ok {
			files.ServeHTTP(w, r)
			return
		}
		cache := "public, max-age=3600, must-revalidate"
		for _, p := range immutable {
			if strings.HasPrefix(r.URL.Path, p) {
				cache = "public, max-age=31536000, immutable"
				break
			}
		}
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", cache)
		for _, tag := range strings.Split(r.Header.Get("If-None-Match"), ",") {
			if strings.TrimSpace(tag) == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		files.ServeHTTP(w, r)
	})
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	app := newApp()
	demo.Reset(ctx, 15*time.Minute, demos.ResetShared)

	srv := &http.Server{
		Addr:              cmp.Or(os.Getenv("VIA_ADDR"), ":8080"),
		Handler:           newMux(app),
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

	// Close first: Shutdown does not cancel the router's own context, so an
	// open SSE response would hold it until its deadline.
	app.Close()
	shut, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := srv.Shutdown(shut); err != nil {
		log.Print(err)
	}
	return <-serve
}
