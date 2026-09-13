package via

import (
	"errors"
	"log"
	"net/http"
	"net/url"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
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

// Reloader re-reads a unit's data AFTER one of its actions ran and BEFORE the
// response render. It is the fix for via's commonest week-one defect: OnInit
// loads, the handler mutates the store, and the render that answers the action
// still shows what OnInit loaded — a 204 and a UI that never moves.
//
//	func (p *Front) Reload(ctx *via.Ctx) error { p.links = p.store.Front(); return nil }
//	func (p *Front) OnInit(ctx *via.Ctx) error { return p.Reload(ctx) }
//
// Why a second hook and not a second OnInit run: OnInit is an INITIALIZER, not
// a loader. It mints and defaults the session, seeds client signals from the
// request URL, registers Tick/Listen, and may Redirect or return ErrNotFound —
// all of which are wrong to repeat once a handler has already committed a
// mutation. Re-running it would overwrite the very session value the handler
// just Put, and reset a hydrated signal to the ACTION url's (absent) query
// string. Reload says exactly one thing, so it can run exactly when it should.
//
// It runs on the plain path and the live path alike, once per action, and is
// skipped when the handler queued a Redirect (nothing from this render ships).
// ctx.Tick and ctx.Listen are no-ops inside it: liveness is the GET/connect
// verdict (I5). A non-nil error is answered like OnInit's — ErrNotFound is 404,
// anything else 500 — and a Redirect it queues navigates the tab.
type Reloader interface{ Reload(*Ctx) error }

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
	// Resolve once, eagerly, even with no OnInit: Embed copies the parent's
	// handle, so a nil here lets each child resolve its own and every Put mint
	// its own id — two sibling embeds writing the session sent two Set-Cookie
	// headers and orphaned the first. Resolving alone reads the cookie and
	// never writes one (only Session.ensure mints), so this cannot create a
	// session for a request that would not otherwise touch one.
	ctx.Session()
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

// reloadUnit runs v's Reload after an action, on a Ctx that registers nothing
// (I5 — liveness is the GET/connect verdict and a reload may not raise it).
//
// Nothing is written to w: a Redirect it queues and an error it returns are the
// caller's to answer, because the right answer differs per transport (a @post
// navigates by script, a native submit by 303).
func reloadUnit(v any, ctx *Ctx) (err error) {
	ic, ok := v.(Reloader)
	if !ok {
		return nil
	}
	ctx.reinit, ctx.initDone = true, true
	// A paramMiss means the URL segment no longer decodes — the same 404 the
	// first OnInit would have answered.
	defer func() {
		if rec := recover(); rec != nil {
			if _, isMiss := rec.(paramMiss); !isMiss {
				panic(rec)
			}
			err = ErrNotFound
		}
	}()
	return ic.Reload(ctx)
}

// answerReloadFailure answers a failed Reload the way runOnInit answers a
// failed OnInit. A queued Redirect is NOT handled here: the caller
// routes it through respond, which knows the transport.
func answerReloadFailure(w http.ResponseWriter, err error) {
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("via: Reload after an action failed: %q", err)
	http.Error(w, "init failed", http.StatusInternalServerError)
}

// checkViewReceiver panics on a composition whose View has a VALUE receiver
// while it holds Signals. View is then called on a copy, so every Signal it
// binds offsets from a stack address the render throws away — which used to
// surface as a per-request 500 forever, once per request, with the process
// serving happily. This makes it a Mount/Embed-time panic instead: fail at
// boot, not per request.
func checkViewReceiver(t reflect.Type) {
	if _, done := valueReceiverChecked.Load(t); done {
		return
	}
	if t != nil && t.Implements(viewerType) && len(signalsOf(t).fields) > 0 {
		panic("via: " + t.String() + ".View has a VALUE receiver and the composition holds Signals — " +
			"View must take a POINTER receiver (func (p *" + t.Name() + ") View() h.H), or every " +
			"rendered Signal binds against a discarded copy")
	}
	valueReceiverChecked.Store(t, true)
}

var valueReceiverChecked sync.Map // reflect.Type -> true

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
	// noChange dedupes the dead-click warning per action (see warnNoChange).
	// Per Router, not per process, so a second app in the same binary — or a
	// second test — still gets told.
	noChange sync.Map
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
	checkViewReceiver(rootType)
	signalsOf(rootType) // walk the type at Mount, so a mis-held Signal fails at boot and not per request
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
		liveCount: r.liveCount, maxLive: r.maxLive, noChange: &r.noChange,
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
