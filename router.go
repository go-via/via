package via

import (
	"bytes"
	"encoding/json"
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
func runOnInit(v any, w http.ResponseWriter, req *http.Request, sessions *sessionManager) {
	ic, ok := v.(Initer)
	if !ok {
		return
	}
	ctx := newCtx(nil)
	ctx.req = req
	ctx.sessions = sessions
	ctx.sessW = w
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
}](r *Router, path string, root T) {
	base := strings.TrimSuffix(path, "/") // "" for "/", "/profile" for "/profile"
	getPattern := path
	if path == "/" {
		getPattern = "/{$}"
	}
	r.mux.HandleFunc("GET "+getPattern, func(w http.ResponseWriter, req *http.Request) {
		inst := root
		runOnInit(PT(&inst), w, req, r.sessions) // load session/request data into fields first
		_, body := renderRootBase(PT(&inst), nil, false, true, base)
		writeHTMLPage(w, r.cfg, body)
	})
	r.mux.HandleFunc("POST "+base+"/_via/a/{n}", func(w http.ResponseWriter, req *http.Request) {
		statelessAction[T, PT](w, req, root, r.cfg, r.sessions, base)
	})
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
}](w http.ResponseWriter, req *http.Request, root T, cfg *config, sessions *sessionManager, base string) {
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
	dispatchStateless[T, PT](w, req, root, cfg, sessions, base, in)
}

// dispatchStateless is the post-decode core of a stateless action: bind the
// render (base-aware), guard the render shape, run the positional action, and
// element-patch the change (or 204). Shared by the router's statelessAction and
// the single-page Register, so the two can't drift. The caller has already run
// the origin floor, body decode, and panic-recover.
func dispatchStateless[T any, PT interface {
	*T
	viewer
}](w http.ResponseWriter, req *http.Request, root T, cfg *config, sessions *sessionManager, base string, in map[string]json.RawMessage) {
	inst := root
	runOnInit(PT(&inst), w, req, sessions) // load session/request data before the action + re-render
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
	bind.actions[n]()
	_, after := renderRootBase(PT(&inst), nil, false, true, base)
	if bytes.Equal(before, after) {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeSecurityHeaders(w, genCSPNonce())
	w.Write(after)
}
