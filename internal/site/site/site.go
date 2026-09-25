// Package site assembles go-via.dev: the router with every content page
// mounted on it, and the mux that serves the assets and the probes in front
// of it. Command site wraps it in an http.Server.
package site

import (
	"log"
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/content"
	"go-via.dev/site/shell"
)

// Options is what the site cannot know about itself: the build it was
// stamped with, and where it is deployed.
type Options struct {
	// Version is echoed by /healthz.
	Version string

	// Origin is the site's own origin ("https://go-via.dev"). Set, it turns on
	// origin enforcement and Secure cookies and gives pages a canonical URL;
	// unset, action POSTs are accepted from anywhere and no canonical is
	// written, which is the local-development shape.
	Origin string
}

// New returns the router and the handler in front of it. Close the router when
// the server is done — http.Server.Shutdown does not drain the live half.
func New(opts Options) (*via.Router, http.Handler) {
	app := newApp(opts.Origin)
	return app, newMux(app, opts.Version)
}

func errorPage(ctx *via.Ctx, e via.PageError) h.H {
	title, detail := "Something broke", e.Detail
	switch e.Reason {
	case via.ReasonNotFound:
		title, detail = "Not found", "That URL is not part of the docs."
	case via.ReasonBadRequest, via.ReasonForbidden, via.ReasonMethodNotAllowed, via.ReasonGone:
		title = "Request refused"
	case via.ReasonTooLarge:
		title = "Request too large"
	case via.ReasonUnavailable:
		title, detail = "Temporarily unavailable", "The session store did not answer. Nothing you did caused this; try again shortly."
	}
	return shell.Page(shell.NavItem{Title: title},
		h.P(h.Str(detail)),
		h.P(h.A(h.Href("/"), h.Str("Back to the front page"))),
	)
}

func routerOptions(origin string) []via.Option {
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
		// One VPS: each stream holds a composition tree and up to fifty rows
		// per live list, and a connect costs the client nothing, so the default
		// of ten thousand is an OOM lever. The cap 503s connects router-wide,
		// so it is also the availability ceiling.
		via.WithMaxSSEConn(1000),
	}
	if origin != "" {
		opts = append(opts, via.WithTrustedOrigin(origin), via.WithSecureCookies())
	} else {
		log.Print("VIA_ORIGIN unset: every origin is accepted and cookies are not Secure")
	}
	return opts
}

func newApp(origin string) *via.Router {
	app := via.NewRouter(routerOptions(origin)...)

	via.Mount(app, "/", content.NewLanding(origin))
	via.Mount(app, "/actions", content.NewActions(origin))
	via.Mount(app, "/signals", content.NewSignals(origin))
	via.Mount(app, "/live", content.NewLive(origin))
	via.Mount(app, "/islands", content.NewIslands(origin))
	via.Mount(app, "/platform", content.NewPlatform(origin))
	via.Mount(app, "/deploy", content.NewDeploy(origin))
	via.Mount(app, "/reference", content.NewReference(origin))

	return app
}

func newMux(app http.Handler, version string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", staticHandler())
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write([]byte("ok " + version + "\n"))
	})
	mux.HandleFunc("GET /robots.txt", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.Header().Set("Cache-Control", "public, max-age=3600")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write([]byte("User-agent: *\nAllow: /\n"))
	})
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/static/brand/icon-amber-ink.svg", http.StatusMovedPermanently)
	})
	mux.Handle("/", app)
	return mux
}
