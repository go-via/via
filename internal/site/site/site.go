// Package site assembles go-via.dev: the router with every content page
// mounted on it, and the mux that serves the assets and the probes in front
// of it. Command site wraps it in an http.Server.
package site

import (
	"html"
	"log"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"go-via.dev/site/content"
	"go-via.dev/site/icon"
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

func errorPage(s *shell.Site, idx *search.Index) func(*via.Ctx, via.PageError) h.H {
	return func(ctx *via.Ctx, e via.PageError) h.H {
		if e.Reason == via.ReasonNotFound {
			return notFound(s, idx, ctx.Request())
		}
		title, detail := "Something broke", e.Detail
		switch e.Reason {
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

// notFound offers the closest nav entry and a search. The error page carries
// no composition, so the live search box cannot run here; the form is a plain
// GET back to the same URL, answered from the index on the server. It sits in
// the top bar where the live box would, and in the body for phones, whose top
// bar folds into the drawer.
func notFound(s *shell.Site, idx *search.Index, r *http.Request) h.H {
	body := []h.H{h.P(h.Str("That URL is not part of the docs."))}
	if it, ok := shell.Suggest(strings.TrimPrefix(r.URL.Path, s.Base)); ok {
		body = append(body, h.P(h.Str("Did you mean "), h.A(h.Href(s.Href(it.Path)), h.Str(it.Title)), h.Str("?")))
	}
	q := r.URL.Query().Get("q")
	body = append(body, searchForm("search search-page", "q404-page", q))
	if q != "" {
		hits := idx.Find(q, 8)
		if len(hits) == 0 {
			body = append(body, h.P(h.Class("search-none"), h.Str("No matches")))
		} else {
			items := []h.H{h.Class("search-hits")}
			for _, hit := range hits {
				label := hit.Title
				if hit.Heading != "" {
					label += " › " + hit.Heading
				}
				items = append(items, h.Li(h.A(h.Href(hit.Href), h.Str(label)), h.Small(h.Str(hit.Snippet))))
			}
			body = append(body, h.Ul(items...))
		}
	}
	body = append(body, h.P(h.A(h.Href(s.Href("/")), h.Str("Back to the front page"))))
	return s.Doc(shell.NavItem{Title: "Not found"}, searchForm("search", "q404", q)).Page(body...)
}

func searchForm(class, id, q string) h.H {
	return h.Form(h.Class(class), h.Method("get"), h.Role("search"),
		h.Label(h.Class("sr-only"), h.For(id), h.Str("Search the docs")),
		icon.Search(),
		h.Input(h.ID(id), h.Type("search"), h.Name("q"), h.Value(q), h.Placeholder("Search Via")),
	)
}

func routerOptions(s *shell.Site, idx *search.Index) []via.Option {
	opts := []via.Option{
		via.WithHead(via.Head{
			Lang: "en",
			// Raw is emitted unparsed, so WithHead panics on a script or
			// style in it: those belong in Assets, where the CSP sees them.
			// theme-color is the bar's colour under 50rem, where browsers tint
			// their own chrome with it.
			Raw: `<meta name="viewport" content="width=device-width, initial-scale=1">` +
				`<meta name="theme-color" content="#1b1e24">` +
				`<link rel="icon" type="image/svg+xml" href="` + s.Asset("/static/brand/icon-amber-ink.svg") + `">` +
				`<link rel="apple-touch-icon" href="` + s.Asset("/static/brand/punch-dark.png") + `">`,
			Assets: via.Assets{
				Styles: []via.Style{{Href: s.Asset("/static/site.css")}, {Href: s.Asset("/static/chroma.css")}},
				Scripts: []via.Script{
					{Src: s.Asset("/static/site.js"), Defer: true},
				},
				// site.css names its fonts relative to its own fingerprinted
				// URL, so the preloads must spell that same URL or the browser
				// fetches each font twice.
				Preload: []via.Preload{
					{Href: fontURL(s, "inter.woff2"), As: "font"},
					{Href: fontURL(s, "jetbrains-mono-400.woff2"), As: "font"},
				},
			},
		}),
		via.WithErrorPage(errorPage(s, idx)),
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
		log.Print("VIA_ORIGIN unset: every origin is accepted and cookies are Secure only over TLS or X-Forwarded-Proto: https")
	}
	return opts
}

func fontURL(s *shell.Site, name string) string {
	return strings.TrimSuffix(s.Asset("/static/site.css"), "site.css") + "fonts/" + name
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
	app := via.NewRouter(routerOptions(s, idx)...)
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
	via.Mount(app, s.Href("/why"), content.NewWhy(env))
	via.Mount(app, s.Href("/examples"), content.NewExamples(env))
	via.Mount(app, s.Href("/tutorial"), content.NewTutorial(env))
	via.Mount(app, s.Href("/compositions"), content.NewCompositions(env))
	via.Mount(app, s.Href("/testing"), content.NewTesting(env))
	via.Mount(app, s.Href("/h"), content.NewHelpers(env))
	via.Mount(app, s.Href("/troubleshooting"), content.NewTroubleshooting(env))
	via.Mount(app, s.Href("/glossary"), content.NewGlossary(env))
	via.Mount(app, s.Href("/_ui"), content.NewStyleguide(env))

	return app
}

func newMux(app http.Handler, s *shell.Site, version string) *http.ServeMux {
	sitemap := s.Origin != "" && s.Base == ""
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
		body := "User-agent: *\nAllow: /\n"
		if sitemap {
			body += "Sitemap: " + s.Origin + "/sitemap.xml\n"
		}
		w.Write([]byte(body))
	})
	if sitemap {
		mux.HandleFunc("GET /sitemap.xml", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/xml; charset=utf-8")
			w.Header().Set("Cache-Control", "public, max-age=3600")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Write(sitemapXML(s))
		})
	}
	if s.Base == "" {
		mux.HandleFunc("GET /latest", latestRedirect(s))
		mux.HandleFunc("GET /latest/", latestRedirect(s))
	}
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, s.Href("/static/brand/icon-amber-ink.svg"), http.StatusMovedPermanently)
	})
	mux.Handle("/", canonicalSlash(app, s))
	return mux
}

// canonicalSlash 308s "/page/" to "/page" for a page that exists, so a
// pasted or hand-typed URL lands on the one the nav, canonical and search
// all name. Anything else falls through to the router's 404.
func canonicalSlash(next http.Handler, s *shell.Site) http.Handler {
	pages := map[string]bool{}
	for _, it := range shell.Pages() {
		if it.Path != "/" {
			pages[s.Href(it.Path)] = true
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := strings.TrimSuffix(r.URL.Path, "/"); p != r.URL.Path && pages[p] {
			u := *r.URL
			u.Path, u.RawPath = p, ""
			http.Redirect(w, r, u.RequestURI(), http.StatusPermanentRedirect)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// latestRedirect sends /latest/<page> to that page on the newest version.
// 302, not 308: the target changes with every release, and a cached
// permanent redirect would pin readers to the old one.
func latestRedirect(s *shell.Site) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page := path.Clean("/" + strings.TrimPrefix(r.URL.Path, "/latest"))
		// path.Clean keeps a backslash, and browsers read a leading /\ as //.
		if strings.Contains(page, `\`) {
			http.NotFound(w, r)
			return
		}
		latest := s.Latest()
		target := latest.Href(page)
		if r.URL.RawQuery != "" && !strings.Contains(target, "://") {
			target += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, target, http.StatusFound)
	}
}

// sitemapXML lists the pages. Only the latest build at the root
// writes one: an older version is noindex, and the URLs must be absolute.
func sitemapXML(s *shell.Site) []byte {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n" +
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")
	for _, it := range shell.Pages() {
		b.WriteString("<url><loc>" + html.EscapeString(s.Origin+s.Href(it.Path)) + "</loc></url>\n")
	}
	b.WriteString("</urlset>\n")
	return []byte(b.String())
}
