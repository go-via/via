// Package site assembles go-via.dev: the router with every content page
// mounted on it, and the mux that serves the assets and the probes in front
// of it. Command site wraps it in an http.Server.
package site

import (
	"log"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/content"
	"go-via.dev/site/search"
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

	// Base is the path prefix the docs are served under: "" for the root, or
	// "/x[/y]" with no trailing slash for an older version sharing the host.
	Base string

	// Versions feeds the version picker, latest first. When set, one entry's
	// Base must equal Base.
	Versions []shell.Version
}

var validBase = regexp.MustCompile(`^(/[A-Za-z0-9._-]+)+$`)

// New returns the router and the handler in front of it. Close the router when
// the server is done — http.Server.Shutdown does not drain the live half.
// Every page is rendered once before it returns, to build the search index,
// so a page that fails to render fails startup.
func New(opts Options) (*via.Router, http.Handler) {
	if opts.Base != "" && !validBase.MatchString(opts.Base) {
		panic("site: malformed Base " + opts.Base + `: want "" or /x[/y] with no trailing slash`)
	}
	s := &shell.Site{Base: opts.Base, Origin: opts.Origin, Versions: opts.Versions}
	if len(s.Versions) > 0 {
		if _, ok := s.Current(); !ok {
			panic("site: no version entry has Base " + `"` + opts.Base + `"`)
		}
	}
	idx := &search.Index{}
	app := newApp(s, idx)
	for _, it := range shell.Pages() {
		rec := httptest.NewRecorder()
		app.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, s.Href(it.Path), nil))
		if rec.Code != http.StatusOK {
			panic("site: indexing " + it.Path + ": status " + http.StatusText(rec.Code))
		}
		idx.Add(search.Extract(it.Title, s.Href(it.Path), rec.Body.String()))
	}
	return app, newMux(app, s, opts.Version)
}

func errorPage(s *shell.Site) func(*via.Ctx, via.PageError) h.H {
	return func(ctx *via.Ctx, e via.PageError) h.H {
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
		return s.Doc(shell.NavItem{Title: title}, nil).Page(
			h.P(h.Str(detail)),
			h.P(h.A(h.Href(s.Href("/")), h.Str("Back to the front page"))),
		)
	}
}

func routerOptions(s *shell.Site) []via.Option {
	opts := []via.Option{
		via.WithHead(via.Head{
			Lang: "en",
			// Raw is emitted unparsed, so WithHead panics on a script or
			// style in it: those belong in Assets, where the CSP sees them.
			Raw: `<meta name="viewport" content="width=device-width, initial-scale=1">` +
				`<link rel="icon" type="image/svg+xml" href="` + s.Href("/static/brand/icon-amber-ink.svg") + `">`,
			Assets: via.Assets{
				Styles:  []via.Style{{Href: s.Href("/static/site.css")}, {Href: s.Href("/static/chroma.css")}},
				Scripts: []via.Script{{Src: s.Href("/static/inspector.js"), Defer: true}},
				Preload: []via.Preload{
					{Href: s.Href("/static/fonts/inter.woff2"), As: "font"},
					{Href: s.Href("/static/fonts/jetbrains-mono-400.woff2"), As: "font"},
				},
			},
		}),
		via.WithErrorPage(errorPage(s)),
		// One VPS: each stream holds a composition tree and up to fifty rows
		// per live list, and a connect costs the client nothing, so the default
		// of ten thousand is an OOM lever. The cap 503s connects router-wide,
		// so it is also the availability ceiling.
		via.WithMaxSSEConn(1000),
	}
	if s.Base != "" {
		// The cookie is host-wide, so two versions on one host would otherwise
		// overwrite each other's session.
		opts = append(opts, via.WithSessionCookieName("via_session_"+cookieSuffix(s.Base)))
	}
	if s.Origin != "" {
		opts = append(opts, via.WithTrustedOrigin(s.Origin), via.WithSecureCookies())
	} else {
		log.Print("VIA_ORIGIN unset: every origin is accepted and cookies are not Secure")
	}
	return opts
}

func cookieSuffix(base string) string {
	return strings.Trim(strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') {
			return r
		}
		return '_'
	}, base), "_")
}

func newApp(s *shell.Site, idx *search.Index) *via.Router {
	app := via.NewRouter(routerOptions(s)...)
	env := content.Env{Site: s, Index: idx}

	via.Mount(app, s.Href("/"), content.NewLanding(env))
	via.Mount(app, s.Href("/start"), content.NewStart(env))
	via.Mount(app, s.Href("/actions"), content.NewActions(env))
	via.Mount(app, s.Href("/signals"), content.NewSignals(env))
	via.Mount(app, s.Href("/live"), content.NewLive(env))
	via.Mount(app, s.Href("/islands"), content.NewIslands(env))
	via.Mount(app, s.Href("/security"), content.NewSecurity(env))
	via.Mount(app, s.Href("/deploy"), content.NewDeploy(env))
	via.Mount(app, s.Href("/reference"), content.NewReference(env))
	via.Mount(app, s.Href("/migrate"), content.NewMigrate(env))

	return app
}

func newMux(app http.Handler, s *shell.Site, version string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("GET "+s.Base+"/static/", http.StripPrefix(s.Base, staticHandler()))
	mux.HandleFunc("GET "+s.Href("/platform"), func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.Href("/security"), http.StatusMovedPermanently)
	})
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
		http.Redirect(w, r, s.Href("/static/brand/icon-amber-ink.svg"), http.StatusMovedPermanently)
	})
	mux.Handle("/", app)
	return mux
}
