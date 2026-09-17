package via

import (
	"context"
	"errors"
	"fmt"
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
//
// Opting in is having the method, so a rename or a signature change opts you
// silently OUT: the composition still compiles and the hook simply stops
// running. Pin it next to the type, and a future rename is a compile error:
//
//	var _ via.Initer = (*Front)(nil)
//
// Mount and Child also catch the two commonest slips — an OnInit with the
// wrong signature panics at boot, and a hook-shaped method with a near-miss
// name (Reload, OnInitialize, …) on a type that implements neither interface
// is logged — but the assertion above is the only airtight form.
type Initer interface{ OnInit(*Ctx) error }

// Reloader re-reads a unit's data AFTER one of its actions ran and BEFORE the
// response render. It is the fix for via's commonest week-one defect: OnInit
// loads, the handler mutates the store, and the render that answers the action
// still shows what OnInit loaded — a 204 and a UI that never moves.
//
//	func (p *Front) OnReload(ctx *via.Ctx) error { p.links = p.store.Front(); return nil }
//	func (p *Front) OnInit(ctx *via.Ctx) error { return p.OnReload(ctx) }
//
// Why a second hook and not a second OnInit run: OnInit is an INITIALIZER, not
// a loader. It mints and defaults the session, registers Tick/Listen, and may
// Redirect or return ErrNotFound — all of which are wrong to repeat once a
// handler has already committed a mutation. Re-running it would overwrite the
// very session value the handler just Put. OnReload says exactly one thing, so
// it can run exactly when it should.
//
// It runs on the plain path and the live path alike, once per action, and is
// skipped when the handler queued a Redirect (nothing from this render ships).
// ctx.Tick and ctx.Listen are no-ops inside it: liveness is the GET/connect
// verdict (I5). A non-nil error is answered like OnInit's — ErrNotFound is 404,
// anything else 500 — and a Redirect it queues navigates the tab.
//
// Like Initer it is duck-typed, so pin it: var _ via.Reloader = (*Front)(nil).
type Reloader interface{ OnReload(*Ctx) error }

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
	ctx.doInit = true // every embedded child's OnInit runs too, inside Child
	// Resolve once, eagerly, even with no OnInit: Child copies the parent's
	// handle, so a nil here lets each child resolve its own and every Put mint
	// its own id — two sibling children writing the session sent two Set-Cookie
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
		noteErr(w, oerr)
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

// reloadUnit runs v's OnReload after an action, on a Ctx that registers nothing
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
	return ic.OnReload(ctx)
}

// answerReloadFailure answers a failed OnReload the way runOnInit answers a
// failed OnInit. A queued Redirect is NOT handled here: the caller
// routes it through respond, which knows the transport.
func answerReloadFailure(w http.ResponseWriter, err error) {
	noteErr(w, err)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	log.Printf("via: OnReload after an action failed: %q", err)
	http.Error(w, "init failed", http.StatusInternalServerError)
}

// checkViewReceiver panics on a composition whose View has a VALUE receiver
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
	if ci, ok := rec.(initOutcome); ok {
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
	route := ""
	if req != nil {
		route = " [" + req.Method + " " + req.URL.Path + "]"
	}
	log.Printf("via: %s panic%s: %v\n%s", what, route, rec, debug.Stack())
	noteErr(w, fmt.Errorf("via: %s panic: %v", what, rec))
	http.Error(w, what+" failed", http.StatusInternalServerError)
}

// answerInitFailure gives an embedded child's failed OnInit the same answers
// the root's gets in runOnInit.
func answerInitFailure(w http.ResponseWriter, req *http.Request, ci initOutcome) {
	switch {
	case ci.redirect != "":
		if !hcore.SafeURL(ci.redirect) {
			log.Printf("via: unsafe OnInit redirect %q dropped", ci.redirect)
			http.Error(w, "init failed", http.StatusInternalServerError)
			return
		}
		http.Redirect(w, req, ci.redirect, http.StatusSeeOther)
	case errors.Is(ci.err, ErrNotFound):
		noteErr(w, ci.err)
		http.Error(w, "not found", http.StatusNotFound)
	default:
		noteErr(w, ci.err)
		log.Printf("via: OnInit failed: %q", ci.err)
		http.Error(w, "init failed", http.StatusInternalServerError)
	}
}

// Router serves several via pages, each Mounted at its own path, behind one
// http.Handler. Sessions are configured on the router (one cookie for the whole
// app) and shared across mounts; each page's actions are namespaced under its
// mount path, so two pages can declare the same action without colliding.
//
// A Router owns goroutines (one per live tab, plus its tickers), so shut it
// down with [Router.Close] — http.Server.Shutdown alone will not, and will
// block on every open SSE stream until its own deadline.
//
// The zero Router is usable and configures itself on first use, like
// http.ServeMux; reach for [NewRouter] whenever you have options to pass.
type Router struct {
	once      sync.Once
	mux       *http.ServeMux
	cfg       *config
	sessions  *sessionManager
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
	// noChange dedupes the dead-click warning per action (see warnNoChange).
	// Per Router, not per process, so a second app in the same binary — or a
	// second test — still gets told.
	noChange sync.Map
	// hookWarned dedupes the near-miss hook warning, per Router for the same
	// reason noChange is.
	hookWarned sync.Map
	// errCSP is the router-wide floor an error page renders under; see
	// WithErrorPage for why it is never a mount's own, wider policy.
	errCSP      string
	errPageWarn sync.Once
}

// NewRouter builds an empty router. Mount pages onto it, then serve it, and
// [Router.Close] it when the server is shutting down. Options (WithSessionKey,
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
		r.mux = http.NewServeMux()
		r.reg = newRegistry()
		r.liveCount = &atomic.Int64{}
		r.maxLive = r.cfg.maxSSEConn
		r.ctx, r.cancel = context.WithCancel(context.Background())
		r.errCSP = buildCSP(r.cfg.head.Assets, Assets{})
		r.mux.HandleFunc("GET /_via/datastar.js", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/javascript")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Write(datastarJS)
		})
	})
}

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

// Close shuts the router's live half down and returns once it is quiet. Call it
// BEFORE http.Server.Shutdown: a stream's goroutine, its Tick timers and its
// Listen subscriptions hang off a context of the router's own, which Shutdown
// does not cancel — so without this Shutdown blocks on every open tab until its
// own deadline expires and then kills them mid-frame.
//
//	srv := &http.Server{Handler: r}
//	…
//	<-stop
//	r.Close()
//	srv.Shutdown(ctx)
//
// Every open stream ends the way a closed tab ends: the handler returns
// normally, so the response terminates cleanly rather than truncating, and
// each unit's OnDispose runs. An action POST in flight against a closing tab
// resolves either as its normal response or as 410 Gone, the same answer it
// gets against a tab that has just disconnected — never silently dropped. A
// connect arriving after Close is refused 503.
//
// Close does NOT stop serving plain pages; that is http.Server.Shutdown's job.
// It is safe to call more than once and from any goroutine, and every call
// waits for the same drain.
func (r *Router) Close() {
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
	r.live.Wait()
}

// Mount registers a page composition at path, in http.ServeMux pattern syntax.
// Its actions post to {path}/_via/a/{child}/{act}. root is taken by value; the
// PT constraint makes a missing or mistyped View() a compile error, like
// Handler.
func Mount[T any, PT ptrViewer[T]](r *Router, path string, root T, opts ...MountOption) {
	r.init(nil)
	mc := &mountConfig{}
	for _, opt := range opts {
		opt(mc)
	}
	patternBase, names := mountBase(path) // "" / "/profile" / "/thread/{id}"
	getPattern := patternBase
	if getPattern == "" {
		getPattern = "/{$}"
	}
	rootType := reflect.TypeOf(root)
	checkViewReceiver(rootType)
	checkHooks(rootType, &r.hookWarned, true)
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
		liveCount: r.liveCount, maxLive: r.maxLive, noChange: &r.noChange, capWarn: &r.capWarn,
		routerCtx: r.ctx, live: &r.live, liveMu: &r.liveMu,
	}
	// Router-then-mount: a mount's own Protect narrows the router's WithGuard
	// chain, never replaces or precedes it.
	m.guards = append(append([]Guard(nil), r.cfg.guards...), mc.guards...)
	// The CSP is derived from the root's declaration ONCE, here, off the
	// zero-data literal: one string per mount, none per request. renderPage
	// re-reads it and panics if the request-time value disagrees.
	lit := root
	assets := pageMetaOf(PT(&lit)).Assets
	assets.validate("via: " + rootType.String() + ".PageMeta().Assets")
	m.csp, m.assetsFP = buildCSP(r.cfg.head.Assets, assets), assets.fingerprint()
	// …and proved constant HERE, not on the first GET. A second reading off a
	// probe copy — the same literal with its zero fields filled in, which is
	// what OnInit does — must produce the same assets. A page that fails this
	// would otherwise boot fine and 500 every request.
	probe := root
	perturbZeroFields(reflect.ValueOf(&probe).Elem(), rootType.PkgPath(), 0)
	fp, read := probeAssets(func() Assets { return pageMetaOf(PT(&probe)).Assets })
	switch {
	case !read:
		log.Printf("via: %s.PageMeta() panicked on synthetic data, so the boot check that "+
			"PageMeta().Assets is constant was SKIPPED for this mount. An asset derived from "+
			"data OnInit loads will not be caught here; it will fail on the first request.",
			reflect.PointerTo(rootType).String())
	case fp != m.assetsFP:
		panic("via: PageMeta().Assets of " + reflect.PointerTo(rootType).String() +
			" depends on the page's data; the CSP is built once at Mount, so assets " +
			"must be a constant of the type. via read the assets twice — once off the " +
			"mounted literal and once off a copy with its zero fields filled in with " +
			"synthetic values, standing in for what OnInit would load — and got two " +
			"different answers. Move the asset into the mounted literal, declare it " +
			"router-wide with WithHead, or hold it in a package-level var.")
	}

	r.mux.HandleFunc("GET "+getPattern, func(w http.ResponseWriter, req *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				recoverToHTTP(w, req, rec, "render")
			}
		}()
		// concreteBase, not patternBase: a page at /job/{id} must advertise
		// /job/7/_via/sse. The pattern would be POSTed literally and 404,
		// leaving every live child under a parametrised mount dead.
		base := concreteBase(patternBase, req, names)
		guard, ok := m.runGuards(w, req, modeNative, false, base)
		if !ok {
			return
		}
		m.writePage(w, req, newInst(), base, nil, guard)
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
// bootstrap and the reconnect manager. via's inline scripts are admitted by
// hash, so no per-response token is threaded through here.
func writeHTMLPage(w http.ResponseWriter, m *mount, body []byte, base string, hasLive bool, root any) {
	meta := pageMetaOf(root)
	if fp := meta.Assets.fingerprint(); fp != m.assetsFP {
		panic("via: PageMeta().Assets of " + typeName(root) +
			" depends on request data; assets must be a constant of the type")
	}
	hdr := w.Header()
	hdr.Set("Content-Type", "text/html; charset=utf-8")
	hdr.Set("X-Content-Type-Options", "nosniff")
	hdr.Set("Content-Security-Policy", m.csp)
	// Pre-declared on every page so the tab id is always defined and always
	// sent (see tabSignal for why the name must stay underscore-free). On a
	// plain page it stays "" and dispatch falls through to the plain path; a
	// click before the stream connects sends an empty id and gets a graceful
	// 410.
	bodyOpen := `</head><body data-signals='{"` + tabSignal + `":""}'>`
	if hasLive {
		// Attribute-escaped here, path-escaped in concreteBase — both layers
		// are needed; see concreteBase.
		bodyOpen = `</head><body data-init="@post('` + hcore.EscapeString(base+"/_via/sse") + `')" data-signals='{"` + tabSignal + `":""}'>`
	}
	var head strings.Builder
	head.WriteString(`<!doctype html>` + m.cfg.head.htmlOpen() + `<head><meta charset="utf-8">`)
	meta.render(&head)
	head.WriteString(m.cfg.head.Raw)
	m.cfg.head.Assets.render(&head)
	meta.Assets.render(&head)
	head.WriteString(`<script type="module" src="/_via/datastar.js"></script>` +
		reconnectScript(hasLive) +
		bodyOpen)
	w.Write([]byte(head.String()))
	w.Write(body)
	w.Write([]byte(`</body></html>`))
}

// hookSpecs are via's optional, duck-typed hooks. Opting in is having the
// method; the cost of that is that a typo or a signature drift opts you
// silently OUT — the composition still compiles and the hook simply never runs.
// rootOnly marks the ones only a MOUNTED page's own methods are read from.
type hookSpec struct {
	name       string
	iface      string
	want       string
	shaped     func(reflect.Type) bool
	implements func(reflect.Type) bool
	rootOnly   bool
}

var hookSpecs = []hookSpec{
	{name: "OnInit", iface: "Initer", want: "func(*via.Ctx) error", shaped: ctxErrShaped, implements: implementsAs[Initer]},
	{name: "OnReload", iface: "Reloader", want: "func(*via.Ctx) error", shaped: ctxErrShaped, implements: implementsAs[Reloader]},
	{name: "PageMeta", iface: "PageMetaer", want: "func() via.Meta", shaped: metaShaped, implements: implementsAs[PageMetaer], rootOnly: true},
}

// hookAliases maps a plausible mis-spelling to the hook it was surely meant to
// be. Only consulted for methods that ALSO have the hook signature, which is
// what keeps an ordinary action handler (func(*Ctx), no return) out of it.
var hookAliases = map[string]string{
	"Init": "OnInit", "Initialize": "OnInit", "Initialise": "OnInit",
	"OnInitialize": "OnInit", "OnInitialise": "OnInit", "OnStart": "OnInit",
	"Reload": "OnReload", "OnReloaded": "OnReload", "Refresh": "OnReload",
	"OnRefresh": "OnReload", "Reinit": "OnReload", "OnReInit": "OnReload",
	"Meta": "PageMeta", "Metadata": "PageMeta", "PageMetadata": "PageMeta",
	"GetPageMeta": "PageMeta", "DocumentMeta": "PageMeta", "PageInfo": "PageMeta",
	"Connect": "OnInit", "OnConnect": "OnInit",
}

// aliasFor resolves a method name to the hook it was surely meant to be. The
// match is case-insensitive and tolerates one edit, because the typos that
// actually happen in the wild ("Oninit", "OnConect") are exactly the ones an
// exact table misses. Only names of 5 characters or more are fuzzy-matched, and
// only methods that ALREADY have a hook's signature ever reach here, so an
// unrelated method has to be a single keystroke off a hook name to trip it.
func aliasFor(method string) (string, bool) {
	if hook, ok := hookAliases[method]; ok {
		return hook, true
	}
	lower := strings.ToLower(method)
	for cand, hook := range aliasCandidates {
		if lower == cand {
			return hook, true
		}
	}
	if len(lower) < 5 {
		return "", false
	}
	for cand, hook := range aliasCandidates {
		if withinOneEdit(lower, cand) {
			return hook, true
		}
	}
	return "", false
}

// aliasCandidates is hookAliases plus the hook names themselves, lowercased.
// A correctly-spelled-but-mis-cased hook ("Oninit") is a typo like any other.
var aliasCandidates = func() map[string]string {
	m := make(map[string]string, len(hookAliases)+len(hookSpecs))
	for alias, hook := range hookAliases {
		m[strings.ToLower(alias)] = hook
	}
	for _, h := range hookSpecs {
		m[strings.ToLower(h.name)] = h.name
	}
	return m
}()

// withinOneEdit reports whether a and b are one insertion, deletion or
// substitution apart (Levenshtein distance <= 1).
func withinOneEdit(a, b string) bool {
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
		if len(a) == len(b) {
			return a[i+1:] == b[i+1:]
		}
		return a[i+1:] == b[i:]
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

var hookSigChecked sync.Map // reflect.Type -> true (only on a CLEAN pass)

// childHookWarned dedupes Child's near-miss warning, which would otherwise
// repeat on every render of the child. Mount passes the Router's own map
// instead, so a second app in the same binary — or a second test — is still
// told (see warnNoChange).
var childHookWarned sync.Map

// checkHooks catches the ways a composition can miss a hook it meant to
// implement. A method literally named OnInit/OnReload/Title/Description with
// the wrong signature is unambiguous, so it panics here at Mount/Child rather
// than serving forever with the hook dead. A near-miss NAME is a heuristic, so
// it only warns — but only when the method carries the exact hook signature and
// the real interface is unsatisfied, which is a shape nothing but the mistake
// produces.
//
// root says whether t is being MOUNTED. Only the root's Title is read, so a
// correctly-shaped one on an embedded child is reported: it is a warning and
// not a panic because the very same type may legitimately be a mounted page
// elsewhere in the app, and panicking would outlaw that.
func checkHooks(t reflect.Type, warned *sync.Map, root bool) {
	if t == nil {
		return
	}
	pt := reflect.PointerTo(t)
	if _, done := hookSigChecked.Load(t); !done {
		for _, hook := range hookSpecs {
			if m, ok := pt.MethodByName(hook.name); ok && !hook.shaped(m.Type) {
				panic("via: " + t.String() + "." + hook.name + " has signature " +
					withoutReceiver(m.Type) + ", not " + hook.want + " — so " + t.String() +
					" does NOT implement via." + hook.iface + " and the hook will never run")
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
				log.Printf("via: %s.%s is ignored — only the MOUNTED page's %s names the document, "+
					"and %s is embedded. Move it to the root composition, or fold the value into the "+
					"root's own %s.", t.String(), hook.name, hook.name, t.String(), hook.name)
			}
		}
	}
	// Title was the hook PageMeta replaced. A leftover one is dead code that
	// still compiles and still looks like it names the page, so say so.
	if m, ok := pt.MethodByName("Title"); ok && stringShaped(m.Type) && !implementsAs[PageMetaer](pt) {
		log.Printf("via: %s.Title is no longer a via hook — the document is named by "+
			"PageMeta() via.Meta now, so nothing will ever call it. Return via.Meta{Title: …} instead.",
			t.String())
	}
	for i := range pt.NumMethod() {
		m := pt.Method(i)
		name, aliased := aliasFor(m.Name)
		if !aliased {
			continue
		}
		hook := hookByName(name)
		if !hook.shaped(m.Type) || hook.implements(pt) {
			continue
		}
		log.Printf("via: %s.%s looks like a mis-named %s — it has the hook's exact signature "+
			"but %s implements no via.%s, so nothing will ever call it. Rename it, or add "+
			"`var _ via.%s = (*%s)(nil)` so a rename can never silently unhook it again.",
			t.String(), m.Name, hook.name, t.String(), hook.iface, hook.iface, t.Name())
	}
}

// ctxErrShaped reports whether a METHOD type (receiver still in In(0)) is
// func(*Ctx) error.
func ctxErrShaped(mt reflect.Type) bool {
	return mt.NumIn() == 2 && mt.In(1) == reflect.TypeOf((*Ctx)(nil)) &&
		mt.NumOut() == 1 && mt.Out(0) == reflect.TypeOf((*error)(nil)).Elem() &&
		!mt.IsVariadic()
}

// stringShaped reports whether a METHOD type is func() string — the retired
// Title hook's shape, kept only to recognise a leftover one.
func stringShaped(mt reflect.Type) bool {
	return mt.NumIn() == 1 && mt.NumOut() == 1 && mt.Out(0) == reflect.TypeOf("") &&
		!mt.IsVariadic()
}

// metaShaped reports whether a METHOD type is func() via.Meta.
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
// assets derived from it ARE constant (a CDN base handed to the literal is the
// motivating case). Reference kinds are left nil — a PageMeta deriving assets
// from a slice OnInit fills escapes this probe, which is why the render-time
// comparison stays.
//
// Struct fields whose type comes from outside the page's own package are left
// completely alone, because their zero value is load-bearing: writing 1 into a
// sync.Mutex's state word makes the next Lock() a RUNTIME THROW that no recover
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
