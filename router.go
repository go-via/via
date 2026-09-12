package via

import (
	"errors"
	"log"
	"net/http"
	"runtime/debug"
	"strings"
	"sync/atomic"

	"github.com/go-via/via/internal/hcore"
)

// Initer is an optional per-request hook on a page: OnInit runs with a Ctx
// BEFORE the (ctx-free) View, so a stateless page can load request/session data
// (the logged-in user, a query value) into its fields for rendering. It is the
// stateless analogue of OnConnect for live islands, detected by interface
// assertion — never reflection.
type Initer interface{ OnInit(*Ctx) error }

// ErrNotFound is the sentinel an OnInit (or OnConnect) returns when the data
// the page needs no longer exists — the world changed, the request is honest,
// so the answer is a 404, not a 500. Wrap it freely; errors.Is matches.
var ErrNotFound = errors.New("via: not found")

// errRedirected is runOnInit's internal signal that it already answered the
// request with a 303 from a Redirect the OnInit hook queued — the caller
// must stop, same as any other non-nil return.
var errRedirected = errors.New("via: redirected")

// runOnInit calls v.OnInit with a request-scoped Ctx if v implements Initer.
// sessW is the open response, so OnInit may also set the session cookie or
// queue a Redirect (honoured here with a 303, before the View ever renders —
// this is via's one per-request gate, replacing the removed guard
// mechanism). A non-nil error has already been answered on w (404 for
// ErrNotFound, 500 otherwise, 303 for a redirect) — the caller must stop,
// never render.
func runOnInit(v any, w http.ResponseWriter, req *http.Request, sessions *sessionManager) (err error) {
	ic, ok := v.(Initer)
	if !ok {
		return nil
	}
	// A Param read on a segment that doesn't decode (/thread/abc as Param[int])
	// panics with the paramMiss sentinel — recovered here into a 404: the URL
	// names a page that doesn't exist. Any other panic keeps propagating.
	defer func() {
		if rec := recover(); rec != nil {
			if _, isMiss := rec.(paramMiss); !isMiss {
				panic(rec)
			}
			http.Error(w, "not found", http.StatusNotFound)
			err = ErrNotFound
		}
	}()
	ctx := newCtx(nil)
	ctx.req = req
	ctx.sessions = sessions
	ctx.sessW = w
	defer func() {
		if err == nil && ctx.redirect != "" {
			if !hcore.SafeURL(ctx.redirect) {
				log.Printf("via: unsafe OnInit redirect %q dropped", ctx.redirect)
				http.Error(w, "init failed", http.StatusInternalServerError)
			} else {
				http.Redirect(w, req, ctx.redirect, http.StatusSeeOther)
			}
			err = errRedirected
		}
	}()
	if oerr := ic.OnInit(ctx); oerr != nil {
		if errors.Is(oerr, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
		} else {
			log.Printf("via: OnInit failed: %q", oerr)
			http.Error(w, "init failed", http.StatusInternalServerError)
		}
		return oerr
	}
	return nil
}

// connectError answers a failed OnConnect: ErrNotFound → 404 (the island's
// data is gone — an honest miss), anything else → 500 (a server fault, logged).
func connectError(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("via: OnConnect failed: %q", err)
	http.Error(w, "connect failed", http.StatusInternalServerError)
}

// recoverToHTTP answers a recovered panic on a request transport: the paramMiss
// sentinel (a URL segment that doesn't decode) is an honest 404; anything else
// is a server fault — logged with its stack, answered 500.
func recoverToHTTP(w http.ResponseWriter, rec any, what string) {
	if _, ok := rec.(paramMiss); ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if bad, ok := rec.(badActionArg); ok {
		http.Error(w, "bad action arg: "+bad.err.Error(), http.StatusBadRequest)
		return
	}
	log.Printf("via: %s panic: %v\n%s", what, rec, debug.Stack())
	http.Error(w, what+" failed", http.StatusInternalServerError)
}

// Router serves several via pages, each Mounted at its own path, behind one
// http.Handler — the multi-page story. Sessions are configured on the router
// (one cookie for the whole app) and shared across mounts; each page's actions
// are namespaced under its mount path so two pages can both declare action 1
// without colliding.
type Router struct {
	mux       *http.ServeMux
	cfg       *config
	sessions  *sessionManager
	reg       *registry     // live connections (tab id → island goroutine), app-wide
	liveCount *atomic.Int64 // concurrent live SSE streams, capped at maxLive
	maxLive   int
}

// NewRouter builds an empty router. Mount pages onto it (r.Mount), then serve
// it. Options (WithSessionKey, WithTrustedOrigin, …) configure the whole app.
func NewRouter(opts ...Option) *Router {
	cfg := newConfig(opts)
	sm := newSessionManager(cfg)
	r := &Router{mux: http.NewServeMux(), cfg: cfg, sessions: sm,
		reg: newRegistry(), liveCount: &atomic.Int64{}, maxLive: maxSSEConn}
	r.mux.HandleFunc("GET /_via/datastar.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(datastarJS)
	})
	return r
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { r.mux.ServeHTTP(w, req) }

// Mount registers a page composition at path. Its actions — a @post, a
// PostForm submit, or a live unit's — post to
// {path}/_via/a/{island}/{n} (root is island 0). root is taken by value (no
// '&'); the PT constraint makes a missing or mistyped View() a compile error,
// exactly like Register.
func (r *Router) Mount[T any, PT ptrViewer[T]](path string, root T) {
	patternBase, names := mountBase(path) // "" / "/profile" / "/thread/{id}"
	getPattern := patternBase
	if getPattern == "" {
		getPattern = "/{$}"
	}
	// newInst gives every non-generic internal a fresh, correctly-typed root
	// without carrying T/PT past this function.
	newInst := func() viewer { inst := root; return PT(&inst) }
	m := &mount{
		cfg: r.cfg, sessions: r.sessions, reg: r.reg, newInst: newInst,
		patternBase: patternBase, names: names,
		liveCount: r.liveCount, maxLive: r.maxLive,
	}

	r.mux.HandleFunc("GET "+getPattern, func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				recoverToHTTP(w, rec, "render")
			}
		}()
		inst := newInst()
		if runOnInit(inst, w, req, r.sessions) != nil { // load session/request data into fields first
			return
		}
		ctx, body := renderRootBase(inst, nil, true, concreteBase(patternBase, req, names), nil)
		ctx.islandV = inst // the root is a unit like any embedded island, when it is Live
		units := liveUnits(ctx)
		writeHTMLPage(w, r.cfg, body, len(units) > 0, patternBase+"/_via/sse")
	})
	r.mux.HandleFunc("POST "+patternBase+"/_via/a/{island}/{n}", m.dispatch)
	r.mux.HandleFunc("POST "+patternBase+"/_via/sse", m.connect)
}

// mountBase turns a mount path into a ServeMux base and the names of its
// {name} segments, in declaration order — Go's own ServeMux syntax, no
// rewrite layer. concreteBase resolves the names per request. "/" → "".
func mountBase(path string) (base string, names []string) {
	if path == "/" {
		return "", nil
	}
	for _, seg := range strings.Split(path, "/") {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			names = append(names, seg[1:len(seg)-1])
		}
	}
	return strings.TrimSuffix(path, "/"), names
}

// concreteBase fills the pattern base's {name} segments with this request's
// values so a mounted page's action/form URLs point at the concrete path
// (/thread/5/_via/a/0), not the pattern (/thread/{id}/…).
func concreteBase(patternBase string, req *http.Request, names []string) string {
	b := patternBase
	for _, n := range names {
		b = strings.Replace(b, "{"+n+"}", req.PathValue(n), 1)
	}
	return b
}

// writeHTMLPage writes a page's full HTML document — the datastar module under
// the strict CSP, then the rendered body. A live page also gets the SSE
// bootstrap and the reconnect manager. via's inline scripts are admitted by
// hash, so no per-response token has to be threaded through here.
func writeHTMLPage(w http.ResponseWriter, cfg *config, body []byte, hasLive bool, sseURL string) {
	writeHeadersWithCSP(w, cfg.csp)
	// A live page bootstraps the SSE stream on init and pre-declares the
	// _viatab local signal so $_viatab is always defined: the patch-signals
	// frame fills it with the real tab id; a click before the stream connects
	// sends an empty id and gets a graceful 410.
	bodyOpen := `</head><body>`
	if hasLive {
		bodyOpen = `</head><body data-init="@post('` + sseURL + `')" data-signals='{"_viatab":""}'>`
	}
	var head strings.Builder
	head.WriteString(`<!doctype html>` + cfg.head.htmlOpen() + `<head><meta charset="utf-8">`)
	cfg.head.render(&head)
	head.WriteString(`<script type="module" src="/_via/datastar.js"></script>` +
		reconnectScript(hasLive) +
		bodyOpen)
	w.Write([]byte(head.String()))
	w.Write(body)
	w.Write([]byte(`</body></html>`))
}
