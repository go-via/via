package via

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"unsafe"

	"github.com/go-via/via/internal/hcore"
)

// Initer is via's one lifecycle hook, on a page or on any embedded child:
// OnInit runs with a Ctx BEFORE the (ctx-free) View, so a unit can load
// request/session data (the logged-in user, a query value) into its fields for
// rendering, and can register the timers and subscriptions that make it LIVE —
// ctx.Tick and ctx.Listen are valid only here. Detected by interface
// assertion, never reflection.
//
// Registering a Tick or a Listen is one of the two things that make a unit
// live (rendering a State or List is the other); a unit that does neither is a
// plain request/response page, and OnInit is just its data-loading hook.
type Initer interface{ OnInit(*Ctx) error }

// ErrNotFound is the sentinel an OnInit returns when the data
// the page needs no longer exists — the world changed, the request is honest,
// so the answer is a 404, not a 500. Wrap it freely; errors.Is matches.
var ErrNotFound = errors.New("via: not found")

// errRedirected is runOnInit's internal signal that it already answered the
// request with a 303 from a Redirect the OnInit hook queued — the caller
// must stop, same as any other non-nil return.
var errRedirected = errors.New("via: redirected")

// runOnInit calls v.OnInit on ctx — the SAME Ctx the render then binds, so a
// Tick or Listen it registers is what marks the unit live. sessW is the open
// response, so OnInit may also set the session cookie or queue a Redirect
// (honoured here with a 303, before the View ever renders — this is via's one
// per-request gate, replacing the removed guard mechanism). A non-nil error has
// already been answered on w (404 for ErrNotFound, 500 otherwise, 303 for a
// redirect) — the caller must stop, never render.
func runOnInit(v any, ctx *Ctx, w http.ResponseWriter, req *http.Request, sessions *sessionManager) (err error) {
	ctx.req = req
	ctx.sessions = sessions
	ctx.sessW = w
	ctx.doInit = true // every embedded child's OnInit runs too, inside Embed
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
	oerr := ic.OnInit(ctx)
	ctx.initDone = true // ticks/subs are snapshotted from here on — see Tick/Listen
	if oerr != nil {
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

// recoverToHTTP answers a recovered panic on a request transport: the paramMiss
// sentinel (a URL segment that doesn't decode) is an honest 404; the childInit
// sentinel is an embedded child's failed OnInit, answered exactly as the root's
// would be; anything else is a server fault — logged with its stack, answered 500.
func recoverToHTTP(w http.ResponseWriter, req *http.Request, rec any, what string) {
	if _, ok := rec.(paramMiss); ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if ci, ok := rec.(childInit); ok {
		answerInitFailure(w, req, ci)
		return
	}
	if bad, ok := rec.(badActionArg); ok {
		http.Error(w, "bad action arg: "+bad.err.Error(), http.StatusBadRequest)
		return
	}
	if un, ok := rec.(unrenderedArg); ok {
		http.Error(w, un.body(), http.StatusGone)
		return
	}
	log.Printf("via: %s panic: %v\n%s", what, rec, debug.Stack())
	http.Error(w, what+" failed", http.StatusInternalServerError)
}

// answerInitFailure gives an embedded child's failed OnInit the same answers
// the root's gets in runOnInit: a queued Redirect is a 303, ErrNotFound a 404,
// anything else a logged 500.
func answerInitFailure(w http.ResponseWriter, req *http.Request, ci childInit) {
	switch {
	case ci.redirect != "":
		if !hcore.SafeURL(ci.redirect) {
			log.Printf("via: unsafe OnInit redirect %q dropped", ci.redirect)
			http.Error(w, "init failed", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, req, ci.redirect, http.StatusSeeOther)
	case errors.Is(ci.err, ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	default:
		log.Printf("via: OnInit failed: %q", ci.err)
		http.Error(w, "init failed", http.StatusInternalServerError)
	}
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
	reg       *registry     // streams (tab id → stream goroutine), app-wide
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
// {path}/_via/a/{embed}/{act} (root is embed "r"). root is taken by value (no
// '&'); the PT constraint makes a missing or mistyped View() a compile error,
// exactly like Register.
func (r *Router) Mount[T any, PT ptrViewer[T]](path string, root T) {
	patternBase, names := mountBase(path) // "" / "/profile" / "/thread/{id}"
	getPattern := patternBase
	if getPattern == "" {
		getPattern = "/{$}"
	}
	rootType := reflect.TypeOf(root)
	// newInst gives every non-generic internal a fresh, correctly-typed root
	// without carrying T/PT past this function.
	newInst := func() instance {
		inst := root
		return instance{v: PT(&inst), base: unsafe.Pointer(&inst), size: unsafe.Sizeof(inst),
			typ: rootType, sig: signalsOf(rootType)}
	}
	m := &mount{
		cfg: r.cfg, sessions: r.sessions, reg: r.reg, newInst: newInst,
		patternBase: patternBase, names: names,
		liveCount: r.liveCount, maxLive: r.maxLive,
	}

	r.mux.HandleFunc("GET "+getPattern, func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				recoverToHTTP(w, req, rec, "render")
			}
		}()
		// concreteBase, not patternBase: a page mounted at /job/{id} must
		// advertise /job/7/_via/sse. The pattern would be POSTed literally by
		// the browser and 404, leaving every live embed under a parametrised
		// mount dead.
		m.writePage(w, req, newInst(), concreteBase(patternBase, req, names), nil)
	})
	r.mux.HandleFunc("POST "+patternBase+"/_via/a/{embed}/{act}", m.dispatch)
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
		// PathEscape, not the raw decoded value: this base is concatenated into
		// a Datastar expression inside an HTML attribute (@post('<base>/…')),
		// and Datastar evaluates that expression as JavaScript. A segment
		// containing a quote would otherwise close the string literal and run
		// whatever follows. Escaping here also keeps the URL a valid path, and
		// the value round-trips through Param unchanged because the browser
		// decodes it back before it reaches the mux.
		b = strings.Replace(b, "{"+n+"}", url.PathEscape(req.PathValue(n)), 1)
	}
	return b
}

// writeHTMLPage writes a page's full HTML document — the datastar module under
// the strict CSP, then the rendered body. A streaming page also gets the SSE
// bootstrap and the reconnect manager. via's inline scripts are admitted by
// hash, so no per-response token has to be threaded through here.
func writeHTMLPage(w http.ResponseWriter, cfg *config, body []byte, hasLive bool, sseURL string) {
	writeHeadersWithCSP(w, cfg.csp)
	// Every page pre-declares the viatab signal so it is always defined and
	// always sent: Datastar ships the whole signal store with every @post, and
	// filters out only names matching /(^|\.)_/ — so an ORDINARY name is all
	// it takes for the tab id to ride along, with no per-action header opt and
	// no per-attribute bytes. On a plain page it stays "" and dispatch falls
	// through to the plain path; on a streaming page the patch-signals frame
	// fills it with the real tab id, and a click before the stream connects
	// sends an empty id and gets a graceful 410.
	bodyOpen := `</head><body data-signals='{"` + tabSignal + `":""}'>`
	if hasLive {
		// Escaped as well as path-escaped upstream: path-escaping keeps the
		// value out of the JS string literal, attribute-escaping keeps it out
		// of the attribute. Neither substitutes for the other.
		bodyOpen = `</head><body data-init="@post('` + hcore.EscapeString(sseURL) + `')" data-signals='{"` + tabSignal + `":""}'>`
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
