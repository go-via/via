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

// Initer is via's one lifecycle hook, on a page or any embedded child: OnInit
// runs with a Ctx BEFORE the (ctx-free) View, so a unit can load request or
// session data into its fields and register the timers and subscriptions that
// make it LIVE — ctx.Tick and ctx.Listen are valid only here. Detected by
// interface assertion, never reflection.
type Initer interface{ OnInit(*Ctx) error }

// ErrNotFound is the sentinel an OnInit returns when the data the page needs no
// longer exists — the request is honest, so the answer is 404, not 500. Wrap it
// freely; errors.Is matches.
var ErrNotFound = errors.New("via: not found")

// errRedirected means runOnInit already answered with a 303 from a queued
// Redirect; the caller must stop, like any other non-nil return.
var errRedirected = errors.New("via: redirected")

// runOnInit calls v.OnInit on ctx — the SAME Ctx the render then binds, so a
// Tick or Listen it registers marks the unit live. The response is still open,
// so OnInit may set the session cookie or queue a Redirect (303'd here, before
// the View renders — via's one per-request gate). A non-nil error has ALREADY
// been answered on w, so the caller must stop and never render.
func runOnInit(v any, ctx *Ctx, w http.ResponseWriter, req *http.Request, sessions *sessionManager) (err error) {
	ctx.req = req
	ctx.sessions = sessions
	ctx.sessW = w
	ctx.doInit = true // every embedded child's OnInit runs too, inside Embed
	ic, ok := v.(Initer)
	if !ok {
		return nil
	}
	// A paramMiss (a segment that doesn't decode) becomes a 404: the URL names a
	// page that doesn't exist. Any other panic keeps propagating.
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

// recoverToHTTP answers a recovered panic on a request transport: each via
// sentinel gets the status it means, anything else is a server fault — logged
// with its stack, answered 500.
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
// the root's gets in runOnInit.
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
// http.Handler. Sessions are configured on the router (one cookie for the whole
// app) and shared across mounts; each page's actions are namespaced under its
// mount path, so two pages can declare the same action without colliding.
type Router struct {
	mux       *http.ServeMux
	cfg       *config
	sessions  *sessionManager
	reg       *registry // tab id → stream goroutine, app-wide
	liveCount *atomic.Int64
	maxLive   int
}

// NewRouter builds an empty router. Mount pages onto it, then serve it.
// Options (WithSessionKey, WithTrustedOrigin, …) configure the whole app.
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

// Mount registers a page composition at path, in http.ServeMux pattern syntax.
// Its actions post to {path}/_via/a/{embed}/{act}. root is taken by value; the
// PT constraint makes a missing or mistyped View() a compile error, like
// Register.
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
		// concreteBase, not patternBase: a page at /job/{id} must advertise
		// /job/7/_via/sse. The pattern would be POSTed literally and 404,
		// leaving every live embed under a parametrised mount dead.
		m.writePage(w, req, newInst(), concreteBase(patternBase, req, names), nil)
	})
	r.mux.HandleFunc("POST "+patternBase+"/_via/a/{embed}/{act}", m.dispatch)
	r.mux.HandleFunc("POST "+patternBase+"/_via/sse", m.connect)
}

// mountBase turns a mount path into a ServeMux base plus its {name} segments,
// in declaration order. "/" → "".
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
// values, so a mounted page's action/form URLs point at /thread/5/_via/a/0 and
// not the pattern.
//
// TWO-LAYER ESCAPING (the canonical statement; writeHTMLPage refers here). A
// mount base ends up inside a Datastar expression inside an HTML attribute —
// @post('<base>/…') — and Datastar evaluates that expression as JavaScript, so
// it needs BOTH layers and neither substitutes for the other: PathEscape here
// keeps a segment out of the JS string literal (a quote would otherwise close
// it and run whatever follows), and hcore.EscapeString at the write site keeps
// it out of the attribute. PathEscape also keeps the URL a valid path, and the
// value round-trips through Param unchanged because the browser decodes it back
// before it reaches the mux.
func concreteBase(patternBase string, req *http.Request, names []string) string {
	b := patternBase
	for _, n := range names {
		b = strings.Replace(b, "{"+n+"}", url.PathEscape(req.PathValue(n)), 1)
	}
	return b
}

// writeHTMLPage writes a page's full HTML document — the datastar module under
// the strict CSP, then the rendered body. A streaming page also gets the SSE
// bootstrap and the reconnect manager. via's inline scripts are admitted by
// hash, so no per-response token is threaded through here.
func writeHTMLPage(w http.ResponseWriter, cfg *config, body []byte, hasLive bool, sseURL string) {
	writeHeadersWithCSP(w, cfg.csp)
	// Pre-declared on every page so the tab id is always defined and always
	// sent (see tabSignal for why the name must stay underscore-free). On a
	// plain page it stays "" and dispatch falls through to the plain path; a
	// click before the stream connects sends an empty id and gets a graceful
	// 410.
	bodyOpen := `</head><body data-signals='{"` + tabSignal + `":""}'>`
	if hasLive {
		// Attribute-escaped here, path-escaped in concreteBase — both layers
		// are needed; see concreteBase.
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
