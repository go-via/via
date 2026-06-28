package via

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
)

// Initer is an optional per-request hook on a page: OnInit runs with a Ctx
// BEFORE the (ctx-free) View, so a stateless page can load request/session data
// (the logged-in user, a query value) into its fields for rendering. It is the
// stateless analogue of OnConnect for live islands, detected by interface
// assertion — never reflection.
type Initer interface{ OnInit(*Ctx) }

// runOnInit calls v.OnInit with a request-scoped Ctx if v implements Initer.
// sessW is the open response, so OnInit may also set the session cookie.
func runOnInit(v any, w http.ResponseWriter, req *http.Request, sessions *sessionManager, params []string) {
	ic, ok := v.(Initer)
	if !ok {
		return
	}
	ctx := newCtx(nil)
	ctx.req = req
	ctx.sessions = sessions
	ctx.sessW = w
	ctx.params = params
	ic.OnInit(ctx)
}

// Router serves several via pages, each Mounted at its own path, behind one
// http.Handler — the multi-page story. Sessions are configured on the router
// (one cookie for the whole app) and shared across mounts; each page's actions
// are namespaced under its mount path so two pages can both declare action 1
// without colliding.
type Router struct {
	mux      *http.ServeMux
	cfg      *config
	sessions *sessionManager
}

// NewRouter builds an empty router. Mount pages onto it (via.Mount), then serve
// it. Options (WithTheme, WithSessionKey, …) configure the whole app.
func NewRouter(opts ...Option) *Router {
	cfg := newConfig(opts)
	var sm *sessionManager
	if cfg.sessions {
		sm = newSessionManager(cfg)
	}
	r := &Router{mux: http.NewServeMux(), cfg: cfg, sessions: sm}
	r.mux.HandleFunc("GET /_via/datastar.js", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(datastarJS)
	})
	return r
}

func (r *Router) ServeHTTP(w http.ResponseWriter, req *http.Request) { r.mux.ServeHTTP(w, req) }

// Mount registers a page composition at path. Its actions post to
// {path}/_via/a/{n}. root is taken by value (no '&'); the PT constraint makes a
// missing or mistyped View() a compile error, exactly like Register. A free
// function, not a method, because Go methods cannot carry type parameters.
func Mount[T any, PT interface {
	*T
	viewer
}](r *Router, path string, root T, guards ...Guard) {
	patternBase, names := parsePattern(path) // "" / "/profile" / "/thread/{p0}"
	getPattern := patternBase
	if getPattern == "" {
		getPattern = "/{$}"
	}
	r.mux.HandleFunc("GET "+getPattern, func(w http.ResponseWriter, req *http.Request) {
		params := paramsOf(req, names)
		if runGuards(w, req, r.sessions, params, guards) {
			return
		}
		inst := root
		runOnInit(PT(&inst), w, req, r.sessions, params) // load session/request data into fields first
		_, body := renderRootBase(PT(&inst), nil, false, true, concreteBase(patternBase, req, names))
		writeHTMLPage(w, r.cfg, body)
	})
	r.mux.HandleFunc("POST "+patternBase+"/_via/a/{n}", func(w http.ResponseWriter, req *http.Request) {
		params := paramsOf(req, names)
		if runGuards(w, req, r.sessions, params, guards) {
			return
		}
		statelessAction[T, PT](w, req, root, r.cfg, r.sessions, concreteBase(patternBase, req, names), params)
	})
	r.mux.HandleFunc("POST "+patternBase+"/_via/f/{n}", func(w http.ResponseWriter, req *http.Request) {
		params := paramsOf(req, names)
		if runGuards(w, req, r.sessions, params, guards) {
			return
		}
		formAction[T, PT](w, req, root, r.cfg, r.sessions, concreteBase(patternBase, req, names), params)
	})
}

// parsePattern turns a mount path into a ServeMux base and the names of its
// positional params. Anonymous {} segments (no identifier string) become
// internal named wildcards {p0},{p1},… so Go's ServeMux can capture them and so
// the page's action/form sub-routes inherit the same wildcards. "/" → "".
func parsePattern(path string) (base string, names []string) {
	if path == "/" {
		return "", nil
	}
	segs := strings.Split(path, "/")
	for i, s := range segs {
		if s == "{}" {
			name := "p" + strconv.Itoa(len(names))
			segs[i] = "{" + name + "}"
			names = append(names, name)
		}
	}
	return strings.TrimSuffix(strings.Join(segs, "/"), "/"), names
}

// paramsOf reads the captured path-param values in declaration order.
func paramsOf(req *http.Request, names []string) []string {
	if len(names) == 0 {
		return nil
	}
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = req.PathValue(n)
	}
	return out
}

// concreteBase fills the pattern base's {pN} wildcards with this request's values
// so a mounted page's action/form URLs point at the concrete path
// (/thread/5/_via/a/0), not the pattern (/thread/{p0}/…).
func concreteBase(patternBase string, req *http.Request, names []string) string {
	b := patternBase
	for _, n := range names {
		b = strings.Replace(b, "{"+n+"}", req.PathValue(n), 1)
	}
	return b
}

// runGuards runs a mount's guards before OnInit; the first guard that fails
// short-circuits the request with a 303 to its redirect target and returns true
// (handled). A guard sees a request-scoped Ctx (session + params), never the
// render state.
func runGuards(w http.ResponseWriter, req *http.Request, sessions *sessionManager, params []string, guards []Guard) bool {
	if len(guards) == 0 {
		return false
	}
	ctx := newCtx(nil)
	ctx.req = req
	ctx.sessions = sessions
	ctx.sessW = w
	ctx.params = params
	for _, g := range guards {
		if redirect, ok := g(ctx); !ok {
			http.Redirect(w, req, redirect, http.StatusSeeOther)
			return true
		}
	}
	return false
}

// formAction handles a native form POST (PostForm): parse the form, run the
// positional handler, then 303-redirect to a pending Redirect target, or
// re-render the page (so a handler can show validation errors). Server-rendered
// navigation — no Datastar.
func formAction[T any, PT interface {
	*T
	viewer
}](w http.ResponseWriter, req *http.Request, root T, cfg *config, sessions *sessionManager, base string, params []string) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("via: form handler panic: %v\n%s", rec, debug.Stack())
			http.Error(w, "form failed", http.StatusInternalServerError)
		}
	}()
	if !originAllowed(req, cfg) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	// Cap the form body for memory-exhaustion parity with the JSON action path —
	// ParseForm otherwise buffers a urlencoded body without limit.
	req.Body = http.MaxBytesReader(w, req.Body, maxActionBody)
	if err := req.ParseForm(); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
			return
		}
		http.Error(w, "malformed form", http.StatusBadRequest)
		return
	}
	inst := root
	runOnInit(PT(&inst), w, req, sessions, params)
	bind, _ := renderRootBase(PT(&inst), nil, false, true, base) // populate the forms table
	n, err := strconv.Atoi(req.PathValue("n"))
	if err != nil || n < 0 || n >= len(bind.forms) {
		http.Error(w, "no such form", http.StatusGone)
		return
	}
	bind.req = req
	bind.sessions = sessions
	bind.sessW = w
	bind.params = params
	bind.forms[n](bind)
	if bind.redirect != "" {
		http.Redirect(w, req, bind.redirect, http.StatusSeeOther)
		return
	}
	_, body := renderRootBase(PT(&inst), nil, false, true, base)
	writeHTMLPage(w, cfg, body)
}

// writeHTMLPage writes a page's full HTML document — the datastar module under a
// nonce'd CSP, the optional theme, then the rendered body. (The single-page
// Register adds the live bootstrap + reconnect manager; a router page is
// stateless for now.)
func writeHTMLPage(w http.ResponseWriter, cfg *config, body []byte) {
	nonce := genCSPNonce()
	writeSecurityHeaders(w, nonce)
	w.Write([]byte(`<!doctype html><html><head><meta charset="utf-8">` +
		`<script type="module" nonce="` + nonce + `" src="/_via/datastar.js"></script>` +
		themeStyle(cfg.theme, nonce) + `</head><body>`))
	w.Write(body)
	w.Write([]byte(`</body></html>`))
}

// statelessAction dispatches a mounted page's action — the same contract as the
// single-page stateless action (origin floor, body cap, render-shape guard,
// 204-on-no-change element-patch), but base-aware so the re-render keeps its
// action paths under the mount.
func statelessAction[T any, PT interface {
	*T
	viewer
}](w http.ResponseWriter, req *http.Request, root T, cfg *config, sessions *sessionManager, base string, params []string) {
	defer func() {
		if rec := recover(); rec != nil {
			log.Printf("via: action handler panic: %v\n%s", rec, debug.Stack())
			http.Error(w, "action failed", http.StatusInternalServerError)
		}
	}()
	if !originAllowed(req, cfg) {
		http.Error(w, "forbidden origin", http.StatusForbidden)
		return
	}
	in, ok := decodeActionBody(w, req)
	if !ok {
		return
	}
	dispatchStateless[T, PT](w, req, root, cfg, sessions, base, params, in)
}

// dispatchStateless is the post-decode core of a stateless action: bind the
// render (base-aware), guard the render shape, run the positional action, and
// element-patch the change (or 204). Shared by the router's statelessAction and
// the single-page Register, so the two can't drift. The caller has already run
// the origin floor, body decode, and panic-recover.
func dispatchStateless[T any, PT interface {
	*T
	viewer
}](w http.ResponseWriter, req *http.Request, root T, cfg *config, sessions *sessionManager, base string, params []string, in map[string]json.RawMessage) {
	inst := root
	runOnInit(PT(&inst), w, req, sessions, params) // load session/request data before the action + re-render
	bind, before := renderRootBase(PT(&inst), in, false, true, base)
	if !shapeMatches(bind.order, in) {
		http.Error(w, "render-shape mismatch", http.StatusGone)
		return
	}
	n, err := strconv.Atoi(req.PathValue("n"))
	if err != nil || n < 0 || n >= len(bind.actions) {
		http.Error(w, "no such action", http.StatusGone)
		return
	}
	bind.req = req
	bind.sessions = sessions
	bind.sessW = w
	bind.params = params
	bind.actions[n]()
	_, after := renderRootBase(PT(&inst), nil, false, true, base)
	if bytes.Equal(before, after) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeSecurityHeaders(w, genCSPNonce())
	w.Write(after)
}
