package via

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"reflect"
	"runtime/debug"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/go-via/via/internal/hcore"
)

// initer is via's one lifecycle hook, on a page or any embedded child: OnInit
// runs with a Ctx before the (ctx-free) View, so a unit can load request or
// session data into its fields and register the timers and subscriptions that
// make it live — ctx.Tick and ctx.Listen are valid only here. Detected by
// interface assertion, never reflection.
//
// Duck-typed and unexported: opting in is having the method, and checkHooks is
// the safety net for the two ways a unit misses one it meant to have. The
// contract callers read is in the package doc.
type initer interface{ OnInit(*Ctx) error }

// reloader re-reads a unit's data after one of its actions ran and before the
// response render. It is the fix for via's commonest week-one defect: OnInit
// loads, the handler mutates the store, and the render that answers the action
// still shows what OnInit loaded — a 204 and a UI that never moves.
//
// A second hook and not a second OnInit run because OnInit is an initializer,
// not a loader: it mints the session, registers Tick/Listen, and may Redirect
// or return ErrNotFound, all of which are wrong to repeat once a handler has
// committed a mutation. The contract callers read is in the package doc.
type reloader interface{ OnReload(*Ctx) error }

// ErrNotFound is the sentinel an OnInit returns when the data the page needs no
// longer exists — the request is honest, so the answer is 404, not 500. Wrap it
// freely; errors.Is matches.
var ErrNotFound = errors.New("via: not found")

// errRedirected means runOnInit already answered with a 303 from a queued
// Redirect; the caller must stop, like any other non-nil return.
var errRedirected = errors.New("via: redirected")

// runOnInit calls v.OnInit on ctx — the same Ctx the render then binds, so a
// Tick or Listen it registers marks the unit live. The response is still open,
// so OnInit may set the session cookie or queue a Redirect (303'd here, before
// the View renders — via's one per-request gate). A non-nil error has already
// been answered on w, so the caller must stop and never render.
//
// sse marks the SSE connect, the one transport a Redirect cannot navigate:
// fetch follows a 303 and would deliver the target page's HTML as the stream
// body, so a redirect there answers a plain 403 instead.
func runOnInit(v any, ctx *Ctx, w http.ResponseWriter, req *http.Request, sessions *sessionManager, pol *routerPolicy, sse bool) (err error) {
	ctx.req = req
	ctx.sessions = sessions
	ctx.policy = pol
	ctx.sessW = w
	ctx.doInit = true // every embedded child's OnInit runs too, inside Child
	// Resolve once, eagerly, even with no OnInit: Child copies the parent's
	// handle, so a nil here lets each child resolve its own and every Put mint
	// its own id — two sibling children writing the session sent two Set-Cookie
	// headers and orphaned the first. Resolving alone reads the cookie and
	// never writes one (only Session.ensure mints), so this cannot create a
	// session for a request that would not otherwise touch one.
	if storeDown(w, ctx) {
		return ErrStoreDown
	}
	startTracks(ctx, ctx.unitV)
	ic, ok := v.(initer)
	if !ok {
		return nil
	}
	// A paramMiss (a segment that doesn't decode) becomes a 404: the URL names a
	// page that doesn't exist. Any other panic keeps propagating. One defer with
	// the redirect below, so a Redirect queued before the miss is never written
	// ahead of the 404.
	defer func() {
		if rec := recover(); rec != nil {
			if _, isMiss := rec.(paramMiss); !isMiss {
				panic(rec)
			}
			http.Error(w, "not found", http.StatusNotFound)
			err = ErrNotFound
			return
		}
		if err == nil && ctx.redirect.url != "" {
			switch refused := ctx.redirect.refusal(); {
			case sse:
				http.Error(w, "forbidden", http.StatusForbidden)
			case refused != "":
				ctx.logger().Warn("via: OnInit redirect dropped", "redirect", ctx.redirect, "reason", refused)
				http.Error(w, "init failed", http.StatusInternalServerError)
			default:
				http.Redirect(w, req, ctx.redirect.url, http.StatusSeeOther)
			}
			err = errRedirected
		}
	}()
	// Deferred rather than cleared after the call so a paramMiss panic unwinds
	// it too: ticks/subs are snapshotted when OnInit returns — see Tick/Listen.
	defer func() { ctx.inInit = false }()
	ctx.inInit = true
	oerr := ic.OnInit(ctx)
	if oerr != nil {
		noteErr(w, oerr)
		switch {
		case errors.Is(oerr, ErrNotFound):
			http.Error(w, "not found", http.StatusNotFound)
		case errors.Is(oerr, ErrForbidden):
			http.Error(w, "forbidden", http.StatusForbidden)
		default:
			ctx.logger().Error("via: OnInit failed", "err", oerr)
			http.Error(w, "init failed", http.StatusInternalServerError)
		}
		return oerr
	}
	return nil
}

// reloadUnit runs v's OnReload after an action, on a Ctx that registers nothing
// (I5 — liveness is the GET/connect verdict and a reload may not raise it).
//
// Nothing is written to w: a Redirect it queues and an error it returns are the
// caller's to answer, because the right answer differs per transport (a @post
// navigates by script, a native submit by 303).
func reloadUnit(v any, ctx *Ctx) (err error) {
	ic, ok := v.(reloader)
	if !ok {
		return nil
	}
	ctx.reinit = true
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
	return ic.OnReload(ctx)
}

// answerReloadFailure answers a failed OnReload the way runOnInit answers a
// failed OnInit. A queued Redirect is not handled here: the caller
// routes it through respond, which knows the transport.
func answerReloadFailure(log *slog.Logger, w http.ResponseWriter, err error) {
	noteErr(w, err)
	switch {
	case errors.Is(err, ErrNotFound):
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(err, ErrForbidden):
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		log.Error("via: OnReload after an action failed", "err", err)
		http.Error(w, "init failed", http.StatusInternalServerError)
	}
}

// checkViewReceiver panics on a composition whose View has a value receiver
// while it holds Signals. View is then called on a copy, so every Signal it
// binds offsets from a stack address the render throws away — which used to
// surface as a per-request 500 forever, once per request, with the process
// serving happily. This makes it a Mount/Child-time panic instead: fail at
// boot, not per request.
func checkViewReceiver(t reflect.Type) {
	if _, done := valueReceiverChecked.Load(t); done {
		return
	}
	if t != nil && t.Implements(viewerType) && len(signalsOf(t).fields) > 0 {
		panic(hcore.Miswired("via: " + t.String() + ".View has a VALUE receiver and the composition holds Signals — " +
			"View must take a POINTER receiver (func (p *" + t.Name() + ") View() h.H), or every " +
			"rendered Signal binds against a discarded copy"))
	}
	valueReceiverChecked.Store(t, true)
}

var valueReceiverChecked sync.Map // reflect.Type -> true

// slog's built-in handlers escape newlines; a custom handler may not.
var logLineEscaper = strings.NewReplacer("\n", `\n`, "\r", `\r`)

// recoverToHTTP answers a recovered panic on a request transport: each via
// sentinel gets the status it means, anything else is a server fault — logged
// with its stack, answered 500. sse is runOnInit's flag of the same name.
func recoverToHTTP(log *slog.Logger, w http.ResponseWriter, req *http.Request, rec any, what string, sse bool) {
	if _, ok := rec.(paramMiss); ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	if ci, ok := rec.(initOutcome); ok {
		answerInitFailure(log, w, req, ci, sse)
		return
	}
	if bad, ok := rec.(badActionArg); ok {
		http.Error(w, "bad action arg: "+bad.err.Error(), http.StatusBadRequest)
		return
	}
	if un, ok := rec.(unrenderedArg); ok {
		http.Error(w, un.body(log), http.StatusGone)
		return
	}
	attrs := []any{"err", rec, "stack", string(debug.Stack())}
	if req != nil {
		attrs = append(attrs, "method", req.Method, "path", logLineEscaper.Replace(req.URL.Path))
	}
	log.Error("via: "+what+" panic", attrs...)
	noteErr(w, fmt.Errorf("via: %s panic: %v", what, rec))
	http.Error(w, what+" failed", http.StatusInternalServerError)
}

// answerInitFailure gives an embedded child's failed OnInit the same answers
// the root's gets in runOnInit.
func answerInitFailure(log *slog.Logger, w http.ResponseWriter, req *http.Request, ci initOutcome, sse bool) {
	switch {
	case ci.redirect.url != "" && sse:
		http.Error(w, "forbidden", http.StatusForbidden)
	case ci.redirect.url != "":
		if refused := ci.redirect.refusal(); refused != "" {
			log.Warn("via: OnInit redirect dropped", "redirect", ci.redirect, "reason", refused)
			http.Error(w, "init failed", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, req, ci.redirect.url, http.StatusSeeOther)
	case errors.Is(ci.err, ErrNotFound):
		noteErr(w, ci.err)
		http.Error(w, "not found", http.StatusNotFound)
	case errors.Is(ci.err, ErrForbidden):
		noteErr(w, ci.err)
		http.Error(w, "forbidden", http.StatusForbidden)
	default:
		noteErr(w, ci.err)
		log.Error("via: OnInit failed", "err", ci.err)
		http.Error(w, "init failed", http.StatusInternalServerError)
	}
}

// Router serves several via pages, each Mounted at its own path, behind one
// http.Handler. Sessions are configured on the router (one cookie for the whole
// app) and shared across mounts; each page's actions are namespaced under its
// mount path, so two pages can declare the same action without colliding.
//
// A Router owns goroutines (one per live tab, plus its tickers), so shut it
// down with [Router.Shutdown] — http.Server.Shutdown alone will not, and will
// block on every open SSE stream until its own deadline.
//
// The zero Router is usable and configures itself on first use, like
// http.ServeMux; reach for [NewRouter] whenever you have options to pass.
type Router struct {
	once      sync.Once
	mux       *http.ServeMux
	cfg       *config
	sessions  *sessionManager
	policy    *routerPolicy
	reg       *registry // tab id → stream goroutine, app-wide
	liveCount *atomic.Int64
	maxLive   int
	capWarn   atomic.Int64 // see mount.warnAtCapacity
	// ctx bounds every stream this router opens; Close cancels it and waits on
	// live, so a stream's own goroutine, its tickers and its disposers are all
	// finished by the time Close returns.
	ctx    context.Context
	cancel context.CancelFunc
	live   sync.WaitGroup
	// liveMu orders a stream's live.Add against Close's Wait. Without it a
	// connect that has passed the ctx.Done check can Add while Close is parked
	// in Wait, which is a WaitGroup misuse throw.
	liveMu sync.RWMutex
	// drained closes once live has drained. One waiter for every Shutdown, so
	// a call that gives up at its deadline leaves one goroutine behind, not one
	// per call.
	drainOnce sync.Once
	drained   chan struct{}
	// noChange dedupes the dead-click warning per action (see warnNoChange).
	// Per Router, not per process, so a second app in the same binary — or a
	// second test — still gets told.
	noChange    sync.Map
	unknownActs idDedupe // see mount.unknownAction
	// hookWarned dedupes the near-miss hook warning, per Router for the same
	// reason noChange is.
	hookWarned sync.Map
	// errCSP is the router-wide floor an error page renders under; see
	// WithErrorPage for why it is never a mount's own, wider policy.
	errCSP      string
	errPageWarn sync.Once
}

// NewRouter builds an empty router. Mount pages onto it, then serve it, and
// [Router.Shutdown] it when the server is shutting down. Options (WithSessionKey,
// WithTrustedOrigin, …) configure the whole app.
func NewRouter(opts ...Option) *Router {
	r := &Router{}
	r.init(opts)
	return r
}

// init builds the router's state exactly once, so `new(Router)` works like the
// zero http.ServeMux instead of nil-dereferencing at the first Mount.
func (r *Router) init(opts []Option) {
	r.once.Do(func() {
		r.cfg = newConfig(opts)
		r.sessions = newSessionManager(r.cfg)
		r.policy = &routerPolicy{trustedOrigins: r.cfg.trustedOrigins, log: r.cfg.log}
		r.mux = http.NewServeMux()
		r.reg = newRegistry(r.cfg.maxSSEConn)
		r.liveCount = &atomic.Int64{}
		r.maxLive = r.cfg.maxSSEConn
		r.ctx, r.cancel = context.WithCancel(context.Background())
		r.errCSP = buildCSP(r.cfg.head.Assets, Assets{}, r.cfg.unsafeEval).bare()
		r.mux.HandleFunc("GET /_via/datastar.js", func(w http.ResponseWriter, req *http.Request) {
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("ETag", `"`+datastarHash+`"`)
			if req.URL.Query().Get("v") == datastarHash {
				w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=3600, must-revalidate")
			}
			http.ServeContent(w, req, "", time.Time{}, bytes.NewReader(datastarJS))
		})
	})
}

// ServeHTTP lazily runs r.init on first use, so a zero Router (var r via.Router)
// is ready to Mount and serve without an explicit constructor call.
func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	r.init(nil)
	ew := r.wrapForErrorPage(w, req)
	if ew == nil {
		r.mux.ServeHTTP(w, req)
		return
	}
	r.mux.ServeHTTP(ew, req)
	ew.finish()
}

// Shutdown shuts the router's live half down the way http.Server.Shutdown
// shuts its listeners: it refuses new streams (a connect answers 503), ends
// every open one and waits for their goroutines to return. Call it before
// srv.Shutdown, with the same deadline: a stream's goroutine, its Tick timers
// and its Listen subscriptions hang off a context of the router's own, which
// http.Server.Shutdown does not cancel, so without this srv.Shutdown blocks on
// every open tab until its deadline and then kills them mid-frame.
//
//	<-stop
//	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
//	defer cancel()
//	r.Shutdown(ctx)
//	srv.Shutdown(ctx)
//
// Every open stream ends the way a closed tab ends: the handler returns
// normally, so the response terminates cleanly rather than truncating, and
// each unit's OnDispose runs. An action POST in flight against a closing tab
// resolves either as its normal response or as 410 Gone, the same answer it
// gets against a tab that has just disconnected — never silently dropped.
//
// A goroutine cannot be stopped from outside, so a stream whose Tick, Listen
// or action handler is blocked ends only when that handler returns. If ctx
// ends first, Shutdown logs such tabs with their unit type and what each is
// blocked on (the first 20 by name, then one line with the total), and returns
// ctx.Err(); the goroutines finish on their own.
// Handlers that watch [Ctx.Context] return as soon as Shutdown starts.
//
// Shutdown does not stop serving plain pages; that is http.Server.Shutdown's
// job. It is safe to call more than once and from any goroutine.
func (r *Router) Shutdown(ctx context.Context) error {
	r.init(nil)
	r.cancel()
	// Bracket the connect-side Add: once this returns, every Add that raced
	// the cancel has happened, and every later one sees a cancelled ctx and
	// never runs.
	// A barrier, not a critical section: taking the write lock waits out any
	// connect already between the ctx check and its live.Add.
	r.liveMu.Lock()
	//lint:ignore SA2001 see above
	r.liveMu.Unlock()
	r.drainOnce.Do(func() {
		r.drained = make(chan struct{})
		go func() {
			r.live.Wait()
			close(r.drained)
		}()
	})
	// select picks at random when both are ready; a drained router with a
	// done ctx must still answer nil.
	select {
	case <-r.drained:
		return nil
	default:
	}
	select {
	case <-r.drained:
		return nil
	case <-ctx.Done():
		r.logUndrained()
		return ctx.Err()
	}
}

// maxUndrainedNamed caps the per-tab lines a Shutdown that timed out writes;
// ten thousand stuck tabs are one bug, not ten thousand.
const maxUndrainedNamed = 20

func (r *Router) logUndrained() {
	named := 0
	r.reg.each(func(c *tabStream) {
		named++
		if named <= maxUndrainedNamed {
			r.cfg.log.Error("via: Router.Shutdown deadline passed with this tab's stream still running",
				"tab", c.id, "unit", c.unitType(), "blocked", c.wire.blockedOn())
		}
	})
	r.cfg.log.Error("via: Router.Shutdown returned before every stream ended; each ends when its handler returns",
		"open", r.liveCount.Load(), "named", min(named, maxUndrainedNamed))
}

// Close is [Router.Shutdown] with no deadline: it returns only once every
// stream has ended, however long a blocked handler takes.
func (r *Router) Close() {
	_ = r.Shutdown(context.Background())
}

// Mount registers a page composition at path, in http.ServeMux pattern syntax.
// Its actions post to {path}/_via/a/{child}/{act}. root is taken by value; the
// PT constraint makes a missing or mistyped View() a compile error, like
// Handler.
//
// A page serves its own path only, never a subtree: "/" serves "/", and
// "/docs/" serves "/docs/" but not "/docs/intro", and ServeMux redirects a GET
// of "/docs" to it. "/docs" and "/docs/" share one action route, so only one
// of the two can be mounted. {name} wildcards are allowed; {name...} and {$}
// are not, since the page's action and stream routes live under its path, and
// {child} and {act} are reserved for the action route. Mount panics on either.
//
// Mount renders root's View once, as mounted and without OnInit, and panics
// on a wiring mistake that render reaches (two actions sharing an id, an
// interface or ambiguous value-receiver method, h.El("script"), a Signal with
// no slot, a child without a View), rather than answering 500 on the first
// request. Any other panic in that render is ignored. The check is
// best-effort: a mistake behind a branch the zero value skips still panics on
// the first render that takes it.
func Mount[T any, PT ptrViewer[T]](r *Router, path string, root T, opts ...MountOption) {
	r.init(nil)
	verifyMethodTrampoline()
	mc := &mountConfig{}
	for _, opt := range opts {
		opt(mc)
	}
	patternBase, names := mountBase(path) // "" / "/profile" / "/thread/{id}"
	getPattern := patternBase
	if patternBase == "" || strings.HasSuffix(path, "/") {
		// Without the anchor ServeMux would read a trailing slash as a subtree.
		getPattern += "/{$}"
	}
	rootType := reflect.TypeOf(root)
	checkViewReceiver(rootType)
	checkHooks(r.cfg.log, rootType, &r.hookWarned, true)
	signalsOf(rootType) // walk the type at Mount, so a mis-held Signal fails at boot and not per request
	// newInst gives every non-generic internal a fresh, correctly-typed root
	// without carrying T/PT past this function.
	newInst := func() instance {
		inst := root
		return instance{v: PT(&inst), base: unsafe.Pointer(&inst), size: unsafe.Sizeof(inst),
			typ: rootType, sig: signalsOf(rootType)}
	}
	m := &mount{
		cfg: r.cfg, sessions: r.sessions, policy: r.policy, reg: r.reg, newInst: newInst,
		patternBase: patternBase, names: names,
		liveCount: r.liveCount, maxLive: r.maxLive, noChange: &r.noChange, unknownActs: &r.unknownActs, capWarn: &r.capWarn,
		routerCtx: r.ctx, live: &r.live, liveMu: &r.liveMu,
	}
	// The CSP is derived from the root's declaration once, here, off the
	// zero-data literal: one string per mount, none per request. renderPage
	// re-reads it and panics if the request-time value disagrees.
	lit := root
	assets := pageMetaOf(PT(&lit)).Assets
	assets.validate("via: " + rootType.String() + ".PageMeta().Assets")
	m.csp, m.assetsFP = buildCSP(r.cfg.head.Assets, assets, r.cfg.unsafeEval), assets.fingerprint()
	// …and proved constant here, not on the first GET. A second reading off a
	// probe copy — the same literal with its zero fields filled in, which is
	// what OnInit does — must produce the same assets. A page that fails this
	// would otherwise boot fine and 500 every request.
	probe := root
	perturbZeroFields(reflect.ValueOf(&probe).Elem(), rootType.PkgPath(), 0)
	fp, read := probeAssets(func() Assets { return pageMetaOf(PT(&probe)).Assets })
	switch {
	case !read:
		r.cfg.log.Warn(fmt.Sprintf("via: %s.PageMeta() panicked on synthetic data, so the boot check that "+
			"PageMeta().Assets is constant was SKIPPED for this mount. An asset derived from "+
			"data OnInit loads will not be caught here; it will fail on the first request.",
			reflect.PointerTo(rootType).String()))
	case fp != m.assetsFP:
		panic("via: PageMeta().Assets of " + reflect.PointerTo(rootType).String() +
			" depends on the page's data; the CSP is built once at Mount, so assets " +
			"must be a constant of the type. via read the assets twice — once off the " +
			"mounted literal and once off a copy with its zero fields filled in with " +
			"synthetic values, standing in for what OnInit would load — and got two " +
			"different answers. Move the asset into the mounted literal, declare it " +
			"router-wide with WithHead, or hold it in a package-level var.")
	}

	if msg := bootRender(newInst()); msg != "" {
		panic("via: found at Mount, rendering " + reflect.PointerTo(rootType).String() + " as mounted: " + msg)
	}

	r.mux.HandleFunc("GET "+getPattern, func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				recoverToHTTP(r.cfg.log, w, req, rec, "render", false)
			}
		}()
		// concreteBase, not patternBase: a page at /job/{id} must advertise
		// /job/7/_via/sse. The pattern would be POSTed literally and 404,
		// leaving every live child under a parametrised mount dead.
		base := concreteBase(patternBase, req, names)
		m.writePage(w, req, newInst(), base, nil)
	})
	r.mux.HandleFunc("POST "+patternBase+"/_via/a/{child}/{act}", m.dispatch)
	r.mux.HandleFunc("POST "+patternBase+"/_via/sse", m.connect)
}

// mountBase turns a mount path into a ServeMux base plus its {name} segments,
// in declaration order. "/" → "".
func mountBase(path string) (base string, names []string) {
	if path == "/" {
		return "", nil
	}
	for _, seg := range strings.Split(path, "/") {
		if !strings.HasPrefix(seg, "{") || !strings.HasSuffix(seg, "}") {
			continue
		}
		name := seg[1 : len(seg)-1]
		switch {
		case name == "$":
			panic(fmt.Sprintf(`via: Mount path %q: {$} is not supported — a path ending in "/" already matches only itself`, path))
		case strings.HasSuffix(name, "..."):
			panic(fmt.Sprintf("via: Mount path %q: a {name...} wildcard is not supported — "+
				"a page's action and stream routes live under its path", path))
		case name == "child" || name == "act":
			panic(fmt.Sprintf("via: Mount path %q: {%s} is reserved for via's action route; rename the wildcard", path, name))
		}
		names = append(names, name)
	}
	return strings.TrimSuffix(path, "/"), names
}

// concreteBase fills the pattern base's {name} segments with this request's
// values, so a mounted page's action/form URLs point at /thread/5/_via/a/0 and
// not the pattern.
//
// Two-layer escaping (the canonical statement; writeHTMLPage refers here). A
// mount base ends up inside a Datastar expression inside an HTML attribute —
// @post('<base>/…') — and Datastar evaluates that expression as JavaScript, so
// it needs both layers and neither substitutes for the other: PathEscape here
// keeps a segment out of the JS string literal (a quote would otherwise close
// it and run whatever follows), and the attribute escaping at the write site keeps
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
// bootstrap and the reconnect manager. tab is the id that stream will adopt,
// "" for a page with no live unit.
func writeHTMLPage(w http.ResponseWriter, m *mount, body []byte, base string, tab string, root any) {
	hasLive := tab != ""
	meta := pageMetaOf(root)
	if fp := meta.Assets.fingerprint(); fp != m.assetsFP {
		panic("via: PageMeta().Assets of " + typeName(root) +
			" depends on request data; assets must be a constant of the type")
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("X-Content-Type-Options", "nosniff")
	// Fresh per document, never per mount: a nonce shared across responses is
	// a token an injection could learn from one page and replay in the next.
	nonce := randomToken()
	hdr.Set("Content-Security-Policy", m.csp.withNonce(nonce))
	switch {
	case hasLive:
		// The tab id is the page's CSRF token: a cached copy would hand one
		// viewer's tab to the next, whose pre-connect click then runs on it.
		noStore(w)
	case hdr.Get("Cache-Control") == "":
		// Revalidated per view so the nonce stays per document. An app's own
		// value is kept: the security page says what caching a document costs.
		hdr.Set("Cache-Control", "no-cache")
	}
	// Pre-declared on every page so the tab id is always defined and always
	// sent (see tabSignal for why the name must stay underscore-free). On a
	// plain page it stays "" and dispatch falls through to the plain path. A
	// live page carries its id from the first byte, so a click before the
	// stream connects already names the tab it will reach. tab is base64url:
	// nothing in it needs JSON or attribute escaping.
	bodyOpen := `</head><body data-signals='{"` + tabSignal + `":"` + tab + `"}'>`
	if hasLive {
		// data-signals first: Datastar applies an element's attributes in
		// document order, and a data-init ahead of it would post the connect
		// body before the id is in the store. Attribute-escaped here,
		// path-escaped in concreteBase — both layers are needed; see
		// concreteBase.
		bodyOpen = `</head><body data-signals='{"` + tabSignal + `":"` + tab + `"}' data-init="@post('` +
			hcore.EscapeString(base+"/_via/sse") + `')">`
	}
	var head strings.Builder
	head.WriteString(`<!doctype html>` + m.cfg.head.htmlOpen(nonce) + `<head><meta charset="utf-8">`)
	meta.render(&head)
	head.WriteString(m.cfg.head.Raw)
	m.cfg.head.Assets.render(&head)
	meta.Assets.render(&head)
	head.WriteString(`<script type="module" src="/_via/datastar.js?v=` + datastarHash + `"></script>` +
		eventsScript() +
		reconnectScript(hasLive) +
		bodyOpen)
	w.Write([]byte(head.String()))
	w.Write(body)
	w.Write([]byte(`</body></html>`))
}

// hookSpecs are via's optional, duck-typed hooks. Opting in is having the
// method; the cost of that is that a typo or a signature drift opts you
// silently out — the composition still compiles and the hook never runs.
// rootOnly marks the ones only a mounted page's own methods are read from.
type hookSpec struct {
	name       string
	want       string
	shaped     func(reflect.Type) bool
	implements func(reflect.Type) bool
	rootOnly   bool
}

var hookSpecs = []hookSpec{
	{name: "OnInit", want: "func(*via.Ctx) error", shaped: ctxErrShaped, implements: implementsAs[initer]},
	{name: "OnReload", want: "func(*via.Ctx) error", shaped: ctxErrShaped, implements: implementsAs[reloader]},
	{name: "PageMeta", want: "func() via.Meta", shaped: metaShaped, implements: implementsAs[pageMetaer], rootOnly: true},
}

// hookAliases maps a plausible mis-spelling to the hook it was surely meant to
// be. Only consulted for methods that also have the hook signature, which is
// what keeps an ordinary action handler (func(*Ctx), no return) out of it.
var hookAliases = map[string]string{
	"Init": "OnInit", "Initialize": "OnInit", "Initialise": "OnInit",
	"OnInitialize": "OnInit", "OnInitialise": "OnInit", "OnStart": "OnInit",
	"Reload": "OnReload", "OnReloaded": "OnReload", "Refresh": "OnReload",
	"OnRefresh": "OnReload", "Reinit": "OnReload", "OnReInit": "OnReload",
	"Meta": "PageMeta", "Metadata": "PageMeta", "PageMetadata": "PageMeta",
	"GetPageMeta": "PageMeta", "DocumentMeta": "PageMeta", "PageInfo": "PageMeta",
	"Connect": "OnInit",
}

// aliasFor resolves a method name to the hook it was surely meant to be. An
// alias matches case-insensitively; a hook name also matches one slip away
// ("OnRelaod", "Oninit"). The slip tolerance is not extended to the aliases:
// their one-edit neighbours include real names ("Preload" next to "Reload",
// "OnInitialized" next to "OnInitialize").
func aliasFor(method string) (string, bool) {
	lower := strings.ToLower(method)
	for alias, hook := range hookAliases {
		if lower == strings.ToLower(alias) {
			return hook, true
		}
	}
	for _, h := range hookSpecs {
		if oneSlip(lower, strings.ToLower(h.name)) {
			return h.name, true
		}
	}
	return "", false
}

// v07OnConnect reports whether a method name is the v0.7 OnConnect hook or a
// slip of it. It is checked apart from the aliases because it warns even next
// to an OnInit: v0.7 code had both, and v0.8 calls neither the method nor
// anything in its place.
func v07OnConnect(method string) bool {
	return oneSlip(strings.ToLower(method), "onconnect")
}

// oneSlip reports whether a is b or one typing slip from it: an insertion, a
// deletion, a substitution, or two adjacent letters swapped (optimal string
// alignment distance <= 1). Two slips are too many: "OnRelay", "OnInput" and
// "Preload" are two from a hook name and are ordinary methods.
func oneSlip(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(a)-len(b) > 1 {
		return false
	}
	for i := 0; i < len(b); i++ {
		if a[i] == b[i] {
			continue
		}
		if len(a) != len(b) {
			return a[i+1:] == b[i:]
		}
		if a[i+1:] == b[i+1:] {
			return true
		}
		return i+1 < len(a) && a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:]
	}
	return true
}

func hookByName(name string) hookSpec {
	for _, h := range hookSpecs {
		if h.name == name {
			return h
		}
	}
	panic("via: no hook named " + name)
}

func implementsAs[T any](pt reflect.Type) bool {
	return pt.Implements(reflect.TypeOf((*T)(nil)).Elem())
}

var hookSigChecked sync.Map // reflect.Type -> true (only on a clean pass)

// childHookWarned dedupes Child's near-miss warning, which would otherwise
// repeat on every render of the child. Mount passes the Router's own map
// instead, so a second app in the same binary — or a second test — is still
// told (see warnNoChange).
var childHookWarned sync.Map

// checkHooks catches the ways a composition can miss a hook it meant to
// implement. A method literally named OnInit/OnReload/Title/Description with
// the wrong signature is unambiguous, so it panics here at Mount/Child rather
// than serving forever with the hook dead. A near-miss name is a heuristic, so
// it only warns — but only when the method carries the exact hook signature and
// the real interface is unsatisfied, which is a shape nothing but the mistake
// produces.
//
// root says whether t is being mounted. Only the root's Title is read, so a
// correctly-shaped one on an embedded child is reported: it is a warning and
// not a panic because the very same type may legitimately be a mounted page
// elsewhere in the app, and panicking would outlaw that.
func checkHooks(log *slog.Logger, t reflect.Type, warned *sync.Map, root bool) {
	if t == nil {
		return
	}
	pt := reflect.PointerTo(t)
	if _, done := hookSigChecked.Load(t); !done {
		for _, hook := range hookSpecs {
			if m, ok := pt.MethodByName(hook.name); ok && !hook.shaped(m.Type) {
				panic(hcore.Miswired("via: " + t.String() + "." + hook.name + " has signature " +
					withoutReceiver(m.Type) + ", not " + hook.want +
					" — so the hook will never run"))
			}
		}
		hookSigChecked.Store(t, true)
	}
	if _, dup := warned.LoadOrStore(t, true); dup {
		return
	}
	if !root {
		for _, hook := range hookSpecs {
			if hook.rootOnly && hook.implements(pt) {
				log.Warn(fmt.Sprintf("via: %s.%s is ignored — only the MOUNTED page's %s names the document, "+
					"and %s is embedded. Move it to the root composition, or fold the value into the "+
					"root's own %s.", t.String(), hook.name, hook.name, t.String(), hook.name))
			}
		}
	}
	// Title was the hook PageMeta replaced. A leftover one is dead code that
	// still compiles and still looks like it names the page, so say so.
	if m, ok := pt.MethodByName("Title"); ok && stringShaped(m.Type) && !implementsAs[pageMetaer](pt) {
		log.Warn(fmt.Sprintf("via: %s.Title is no longer a via hook — the document is named by "+
			"PageMeta() via.Meta now, so nothing will ever call it. Return via.Meta{Title: …} instead.",
			t.String()))
	}
	for i := range pt.NumMethod() {
		m := pt.Method(i)
		if v07OnConnect(m.Name) && ctxErrShaped(m.Type) {
			log.Warn(fmt.Sprintf("via: %s.%s is shaped like the v0.7 OnConnect hook, which v0.8 never calls. "+
				"Move its work into OnInit: ctx.OnConnect(fn) runs fn once when the stream opens, and "+
				"ctx.Tick or ctx.Listen makes the unit live.", t.String(), m.Name))
			continue
		}
		name, aliased := aliasFor(m.Name)
		if !aliased {
			continue
		}
		hook := hookByName(name)
		if !hook.shaped(m.Type) || hook.implements(pt) {
			continue
		}
		log.Warn(fmt.Sprintf("via: %s.%s looks like a mis-named %s — it has the hook's exact signature "+
			"but %s has no %s %s, so nothing will ever call it. Rename it to %s.",
			t.String(), m.Name, hook.name, t.String(), hook.name, hook.want, hook.name))
	}
}

// ctxErrShaped reports whether mt is func(*via.Ctx) error; NumIn() == 2
// because the receiver still occupies In(0) for a method's reflect.Type.
func ctxErrShaped(mt reflect.Type) bool {
	return mt.NumIn() == 2 && mt.In(1) == reflect.TypeOf((*Ctx)(nil)) &&
		mt.NumOut() == 1 && mt.Out(0) == reflect.TypeOf((*error)(nil)).Elem() &&
		!mt.IsVariadic()
}

// stringShaped reports whether a method type is func() string — the retired
// Title hook's shape, kept only to recognise a leftover one.
func stringShaped(mt reflect.Type) bool {
	return mt.NumIn() == 1 && mt.NumOut() == 1 && mt.Out(0) == reflect.TypeOf("") &&
		!mt.IsVariadic()
}

func metaShaped(mt reflect.Type) bool {
	return mt.NumIn() == 1 && mt.NumOut() == 1 && mt.Out(0) == reflect.TypeOf(Meta{}) &&
		!mt.IsVariadic()
}

// typeName names the composition behind a root pointer for a panic message,
// without reflect: every root reaches here as *T from Mount's own PT.
func typeName(root any) string { return fmt.Sprintf("%T", root) }

func withoutReceiver(mt reflect.Type) string {
	in := make([]string, 0, mt.NumIn()-1)
	for i := 1; i < mt.NumIn(); i++ {
		in = append(in, mt.In(i).String())
	}
	out := make([]string, 0, mt.NumOut())
	for i := range mt.NumOut() {
		out = append(out, mt.Out(i).String())
	}
	s := "func(" + strings.Join(in, ", ") + ")"
	switch len(out) {
	case 0:
	case 1:
		s += " " + out[0]
	default:
		s += " (" + strings.Join(out, ", ") + ")"
	}
	return s
}

// probeAssets reads the probe copy's assets, reporting read=false if PageMeta
// refuses the synthetic data. The probe fills fields with values no author
// promised to accept, so a panic there is the probe's fault, not the page's.
func probeAssets(read func() Assets) (fp string, ok bool) {
	defer func() {
		if recover() != nil {
			fp, ok = "", false
		}
	}()
	return read().fingerprint(), true
}

// perturbZeroFields fills v's zero scalar fields with non-zero values in place,
// standing in for the data OnInit would load. A field the mounted literal
// already set is left alone: that value is fixed for the life of the mount, so
// assets derived from it are constant (a CDN base handed to the literal is the
// motivating case). Reference kinds are left nil — a PageMeta deriving assets
// from a slice OnInit fills escapes this probe, which is why the render-time
// comparison stays.
//
// Struct fields whose type comes from outside the page's own package are left
// completely alone, because their zero value is load-bearing: writing 1 into a
// sync.Mutex's state word makes the next Lock() a runtime throw that no recover
// can catch, and time.Time's wall/ext are just as private. The page's own
// package is the only one whose invariants the author controls.
func perturbZeroFields(v reflect.Value, pkg string, depth int) {
	if depth > 4 || v.Kind() != reflect.Struct {
		return
	}
	for i := range v.NumField() {
		f := v.Field(i)
		if !f.CanAddr() {
			continue
		}
		// NewAt: an unexported field is not settable through reflect, and the
		// fields a page derives its assets from are usually unexported.
		f = reflect.NewAt(f.Type(), unsafe.Pointer(f.UnsafeAddr())).Elem()
		switch f.Kind() {
		case reflect.Struct:
			if foreignStruct(f.Type(), pkg) {
				continue
			}
			perturbZeroFields(f, pkg, depth+1)
		case reflect.String:
			if f.IsZero() {
				f.SetString("via-probe")
			}
		case reflect.Bool:
			if f.IsZero() {
				f.SetBool(true)
			}
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			if f.IsZero() {
				f.SetInt(1)
			}
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
			if f.IsZero() {
				f.SetUint(1)
			}
		case reflect.Float32, reflect.Float64:
			if f.IsZero() {
				f.SetFloat(1)
			}
		}
	}
}

// foreignStruct reports whether a struct type is off-limits to the probe: one
// declared outside the page's own package, or one of the stdlib types whose
// zero value is an invariant even if a page somehow shared their package.
func foreignStruct(t reflect.Type, pkg string) bool {
	switch t.PkgPath() {
	case "sync", "sync/atomic", "time":
		return true
	case pkg:
		return false
	}
	// An anonymous struct (PkgPath "") is written in the page's own source, so
	// it is the author's to perturb.
	return t.PkgPath() != ""
}
