// Package via provides a reactive, real-time engine for creating Go web
// applications. It lets you build live, type-safe web interfaces without
// JavaScript.
//
// Via unifies routing, state, and UI reactivity through a simple mental model:
// Go on the server — HTML in the browser — updated in real time via Datastar.
package via

import (
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/go-via/via/h"
	"github.com/starfederation/datastar-go/datastar"
)

//go:embed datastar.js
var datastarJS []byte

// Internal route constants for Via's HTTP handlers.
const (
	routeDatastarJS   = "GET /_datastar.js"
	routeSSE          = "GET /_sse"
	routeAction       = "GET /_action/{id}"
	routeSessionClose = "POST /_session/close"
)

// session represents internal user session with persistent state
type session struct {
	id         string // tabID - unique per page load/tab
	sessionID  string // cookie-based session ID - shared across tabs
	store      *store
	patchChan  chan patch
	c          *Composition
	lastAccess int64
}

// Middleware is a function that wraps an http.Handler.
type Middleware func(http.Handler) http.Handler

// sessionManager encapsulates all session-related state and synchronization.
type sessionManager struct {
	registry      map[string]*session
	registryMu    sync.RWMutex
	state         map[string]map[string]any // sessionID -> state
	stateMu       sync.RWMutex
	lastAccess    map[string]int64 // sessionID -> last access timestamp
	lastAccessMu  sync.RWMutex
	invalidated   map[string]int64 // sessionID -> invalidation timestamp
	invalidatedMu sync.RWMutex
}

// V is the root application.
// It manages page routing, user sessions, and SSE connections for live updates.
type V struct {
	cfg                  Options
	mux                  *http.ServeMux
	compositionRegistry  map[string]*Composition
	compositionMu        sync.RWMutex
	sessions             *sessionManager
	appState             map[string]any
	appStateMu           sync.RWMutex
	documentHeadIncludes []h.H
	documentFootIncludes []h.H
	documentHTMLAttrs    []h.H
	middlewares          []Middleware
}

func (v *V) logErr(format string, a ...any) {
	log.Printf("[error] msg=%q", fmt.Sprintf(format, a...))
}

func (v *V) logWarn(format string, a ...any) {
	if v.cfg.LogLvl >= LogLevelWarn {
		log.Printf("[warn] msg=%q", fmt.Sprintf(format, a...))
	}
}

func (v *V) logInfo(format string, a ...any) {
	if v.cfg.LogLvl >= LogLevelInfo {
		log.Printf("[info] msg=%q", fmt.Sprintf(format, a...))
	}
}

func (v *V) logDebug(format string, a ...any) {
	if v.cfg.LogLvl == LogLvlDEBUG {
		log.Printf("[debug] msg=%q", fmt.Sprintf(format, a...))
	}
}

// Config overrides the default configuration with the given options.
func (v *V) Config(cfg Options) {
	if cfg.LogLvl != undefined {
		v.cfg.LogLvl = cfg.LogLvl
	}
	if cfg.DocumentTitle != "" {
		v.cfg.DocumentTitle = cfg.DocumentTitle
	}
	if cfg.Plugins != nil {
		for _, plugin := range cfg.Plugins {
			if plugin != nil {
				plugin.Register(v)
			}
		}
	}
	if cfg.DevMode != v.cfg.DevMode {
		v.cfg.DevMode = cfg.DevMode
	}
	if cfg.ServerAddress != "" {
		v.cfg.ServerAddress = cfg.ServerAddress
	}
}

// Use adds middleware to the application.
func (v *V) Use(middleware ...Middleware) {
	v.middlewares = append(v.middlewares, middleware...)
}

// AppendToHead appends the given h.H nodes to the head of the base HTML document.
// Useful for including css stylesheets and JS scripts.
func (v *V) AppendToHead(elements ...h.H) {
	for _, el := range elements {
		if el != nil {
			v.documentHeadIncludes = append(v.documentHeadIncludes, el)
		}
	}
}

// AppendToFoot appends the given h.H nodes to the end of the base HTML document body.
// Useful for including JS scripts.
func (v *V) AppendToFoot(elements ...h.H) {
	for _, el := range elements {
		if el != nil {
			v.documentFootIncludes = append(v.documentFootIncludes, el)
		}
	}
}

// AppendHTMLAttr appends the given h.H nodes as attributes to the HTML element.
func (v *V) AppendHTMLAttr(elements ...h.H) {
	for _, el := range elements {
		if el != nil {
			v.documentHTMLAttrs = append(v.documentHTMLAttrs, el)
		}
	}
}

// Page registers a route and its page composition function.
//
// Example:
//
//	v.Page("/", func(c *via.Composition) {
//		c.View(func(r *via.R) h.H {
//			return h.H1(h.Text("Hello, Via!"))
//		})
//	})
func (v *V) Page(route string, composeFn func(c *Composition)) {
	c := &Composition{
		id:           genRandID(),
		route:        route,
		actions:      make(map[string]func(*Context)),
		actionOwners: make(map[string]compOwner),
	}
	composeFn(c)
	if c.viewFn == nil {
		panic("page " + route + " has no view")
	}
	v.compositionMu.Lock()
	v.compositionRegistry[c.id] = c
	v.compositionMu.Unlock()

	// Register page handler with middleware applied
	pageHandler := v.newPageHTTPHandler(route, c)
	// Apply middleware: last registered runs closest to handler
	var handler http.Handler = http.HandlerFunc(pageHandler)
	for i := len(v.middlewares) - 1; i >= 0; i-- {
		handler = v.middlewares[i](handler)
	}
	v.mux.Handle("GET "+route, handler)

}

func (v *V) newPageHTTPHandler(route string, c *Composition) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")

		tabID, cookieSessionID := v.setupSession(w, r)
		sess := v.initializePageSession(tabID, cookieSessionID, route, r.URL.Path, c)
		ctx := v.createViewContext(sess, cookieSessionID, tabID)
		v.renderPage(w, ctx, c, tabID)
	}
}

// setupSession extracts session ID from cookie or creates new one.
// Returns (tabID, sessionID).
func (v *V) setupSession(w http.ResponseWriter, r *http.Request) (string, string) {
	tabID := genRandID()
	sessionID := v.getSessionIDFromRequest(r)

	if sessionID != "" {
		v.sessions.invalidatedMu.RLock()
		_, invalidated := v.sessions.invalidated[sessionID]
		v.sessions.invalidatedMu.RUnlock()
		if invalidated {
			sessionID = ""
		}
	}

	if sessionID == "" {
		sessionID = genRandID()
		http.SetCookie(w, &http.Cookie{
			Name:     v.cfg.SessionCookieName,
			Value:    sessionID,
			MaxAge:   v.cfg.SessionCookieMaxAge,
			Path:     "/",
			HttpOnly: true,
		})
	}

	return tabID, sessionID
}

// initializePageSession creates session and seeds initial state values.
func (v *V) initializePageSession(tabID, sessionID, route, path string, c *Composition) *session {
	sess := v.getOrCreateSession(tabID)
	sess.sessionID = sessionID
	sess.store.pathParams = extractParams(route, path)
	sess.c = c

	for _, stateReg := range c.states {
		switch stateReg.scope {
		case ScopeTab:
			if _, exists := sess.store.state[stateReg.id]; !exists {
				sess.store.state[stateReg.id] = stateReg.initial
			}
		case ScopeSession:
			if v.sessions.state[sessionID] == nil {
				v.sessions.state[sessionID] = make(map[string]any)
			}
			if _, exists := v.sessions.state[sessionID][stateReg.id]; !exists {
				v.sessions.state[sessionID][stateReg.id] = stateReg.initial
			}
		case ScopeApp:
			if _, exists := v.appState[stateReg.id]; !exists {
				v.appState[stateReg.id] = stateReg.initial
			}
		}
	}

	return sess
}

// createViewContext builds public Context for view rendering.
func (v *V) createViewContext(sess *session, sessionID, tabID string) *Context {
	return &Context{
		store:     sess.store,
		session:   sess,
		mode:      sessionModeView,
		v:         v,
		sessionID: sessionID,
		tabID:     tabID,
		warn:      v.warnFn(),
	}
}

// renderPage builds and renders the complete HTML page.
func (v *V) renderPage(w http.ResponseWriter, ctx *Context, c *Composition, tabID string) {
	headElements := append([]h.H{}, v.documentHeadIncludes...)
	initialSignals := buildInitialSignals(tabID, ctx.store.pathParams, c.signals)

	headElements = append(headElements,
		h.Meta(h.Data("signals", initialSignals)),
		h.Meta(h.Data("init", "@get('/_sse', {retry: 'always'})")),
		h.Meta(h.Data("init", fmt.Sprintf(`window.addEventListener('beforeunload', () => { navigator.sendBeacon('/_session/close', '%s'); })`, tabID))),
	)

	bodyElements := append([]h.H{c.viewFn(ctx)}, v.documentFootIncludes...)
	page := h.HTML5(h.HTML5Props{
		Title:     v.cfg.DocumentTitle,
		Head:      headElements,
		Body:      bodyElements,
		HTMLAttrs: v.documentHTMLAttrs,
	})
	_ = page.Render(w)
}

// buildInitialSignals creates the signal initialization string.
func buildInitialSignals(tabID string, pathParams map[string]string, signals []signalRegistration) string {
	var sb strings.Builder
	fmt.Fprintf(&sb, "{'via-ctx':'%s'", tabID)

	for key, val := range pathParams {
		fmt.Fprintf(&sb, ", '%s':'%s'", key, val)
	}

	for _, sig := range signals {
		fmt.Fprintf(&sb, ", '%s':", sig.id)
		switch v := sig.initial.(type) {
		case string:
			fmt.Fprintf(&sb, "'%s'", v)
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			fmt.Fprintf(&sb, "%d", v)
		case float32, float64:
			fmt.Fprintf(&sb, "%f", v)
		case bool:
			fmt.Fprintf(&sb, "%t", v)
		}
	}

	sb.WriteString("}")
	return sb.String()
}

// Start starts the Via HTTP server on the given address.
func (v *V) Start() {
	v.logInfo("via started at [%s]", v.cfg.ServerAddress)
	log.Fatalf("[fatal] %v", http.ListenAndServe(v.cfg.ServerAddress, v.mux))
}

// HTTPServeMux returns the underlying HTTP request multiplexer to enable user extentions, middleware and
// plugins. It also enables integration with test frameworks like gost-dom/browser for SSE/Datastar testing.
//
// IMPORTANT. The returned *http.ServeMux can only be modified during initialization, before calling via.Start().
// Concurrent handler registration is not safe.
func (v *V) HTTPServeMux() *http.ServeMux {
	return v.mux
}

func (v *V) datastarJSHTTPHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/javascript")
	_, _ = w.Write(datastarJS)
}

func (v *V) sseHTTPHandler(w http.ResponseWriter, r *http.Request) {
	var sigs map[string]any
	_ = datastar.ReadSignals(r, &sigs)

	tabID, _ := sigs["via-ctx"].(string)
	if tabID != "" {
		session, err := v.getSession(tabID)
		if err != nil {
			v.logErr("sse stream failed to start: %v", err)
			return
		}

		sse := datastar.NewSSE(w, r, datastar.WithCompression(
			datastar.WithBrotli(datastar.WithBrotliLevel(5)),
			datastar.WithGzip(),
		))
		sse.Send(datastar.EventTypePatchElements, []string{}, datastar.WithSSEEventId("via-sse-reconnect"))

		v.logDebug("SSE connection established for tab %s", tabID)

		for {
			select {
			case <-sse.Context().Done():
				v.logDebug("SSE connection ended for tab %s", tabID)
				return
			case patch, ok := <-session.patchChan:
				if !ok {
					return
				}
				switch patch.typ {
				case patchTypeElements:
					if err := sse.PatchElements(patch.content); err != nil {
						if sse.Context().Err() == nil {
							v.logErr("PatchElements failed: %v", err)
						}
					}
				case patchTypeSignals:
					if err := sse.PatchSignals([]byte(patch.content)); err != nil {
						if sse.Context().Err() == nil {
							v.logErr("PatchSignals failed: %v", err)
						}
					}
				case patchTypeScript:
					if err := sse.ExecuteScript(patch.content, datastar.WithExecuteScriptAutoRemove(true)); err != nil {
						if sse.Context().Err() == nil {
							v.logErr("ExecuteScript failed: %v", err)
						}
					}
				}
			}
		}
	}

}

func (v *V) actionHTTPHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusNoContent)
	actionID := r.PathValue("id")
	var sigs map[string]any
	_ = datastar.ReadSignals(r, &sigs)

	// Get tabID from via-ctx signal (for tab-scoped state and targeting)
	tabID, _ := sigs["via-ctx"].(string)
	if tabID != "" {
		// Get or create session by tabID (for tab-scoped state)
		sess := v.getOrCreateSession(tabID)

		// Get composition from session
		c := sess.c
		if c == nil {
			v.logDebug("session has no composition")
			return
		}

		if actionFn, exists := c.actions[actionID]; exists {
			// log err if actionFn panics
			defer func() {
				if r := recover(); r != nil {
					v.logErr("action '%s' failed: %v", actionID, r)
				}
			}()

			injectSignals(sess.store, sigs)

			// Extract path params from signals based on route pattern
			pathParamNames := extractParamNames(c.route)
			pathParams := make(map[string]string)
			for _, paramName := range pathParamNames {
				if val, ok := sigs[paramName]; ok {
					if strVal, ok := val.(string); ok {
						pathParams[paramName] = strVal
					}
				}
			}
			sess.store.pathParams = pathParams

			// Get session ID from cookie for session-scoped state
			sessionID := v.getSessionIDFromRequest(r)

			// Create public Context for action (read-write mode)
			sc := &Context{
				store:     sess.store,
				session:   sess,
				mode:      sessionModeAction,
				v:         v,
				sessionID: sessionID,
				tabID:     tabID,
				warn:      v.warnFn(),
			}

			// Check if action belongs to a component
			if c.actionOwners != nil {
				if owner, ok := c.actionOwners[actionID]; ok {
					sc.compID = owner.id
					sc.compViewFn = owner.viewFn
				}
			}

			actionFn(sc)
			return
		}
		v.logDebug("action '%s' not found in session composition", actionID)
		return
	}
	v.logDebug("session not found for tab %q", tabID)
}

func (v *V) sessionCloseHTTPHandler(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		v.logErr("failed to read session close request body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	sessionID := string(body)
	v.removeSession(sessionID)
	v.logDebug("session closed: %s", sessionID)
	w.WriteHeader(http.StatusNoContent)
}

type patchType uint8

const (
	patchTypeElements = iota
	patchTypeSignals
	patchTypeScript
)

type patch struct {
	typ     patchType
	content string
}

// New creates a new *V application with default configuration.
func New() *V {
	mux := http.NewServeMux()

	v := &V{
		mux:                 mux,
		compositionRegistry: make(map[string]*Composition),
		sessions: &sessionManager{
			registry:    make(map[string]*session),
			state:       make(map[string]map[string]any),
			lastAccess:  make(map[string]int64),
			invalidated: make(map[string]int64),
		},
		appState: make(map[string]any),
		cfg: Options{
			DevMode:             false,
			ServerAddress:       ":3000",
			LogLvl:              LogLevelInfo,
			DocumentTitle:       "⚡ Via",
			SessionTTL:          1800, // 30 minutes
			SessionCookieName:   "via_sid",
			SessionCookieMaxAge: 2592000, // 30 days
		},
	}

	v.mux.HandleFunc(routeDatastarJS, v.datastarJSHTTPHandler)

	// Wrap SSE, action, and session handlers with middleware
	var sseHandler http.Handler = http.HandlerFunc(v.sseHTTPHandler)
	var actionHandler http.Handler = http.HandlerFunc(v.actionHTTPHandler)
	var sessionCloseHandler http.Handler = http.HandlerFunc(v.sessionCloseHTTPHandler)

	for i := len(v.middlewares) - 1; i >= 0; i-- {
		sseHandler = v.middlewares[i](sseHandler)
		actionHandler = v.middlewares[i](actionHandler)
		sessionCloseHandler = v.middlewares[i](sessionCloseHandler)
	}

	v.mux.Handle(routeSSE, sseHandler)
	v.mux.Handle(routeAction, actionHandler)
	v.mux.Handle(routeSessionClose, sessionCloseHandler)
	return v
}

func genRandID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)[:32]
}

func isValidHexID(id string) bool {
	if len(id) != 32 {
		return false
	}
	_, err := hex.DecodeString(id)
	return err == nil
}

func extractParams(pattern, path string) map[string]string {
	p := strings.Split(strings.Trim(pattern, "/"), "/")
	u := strings.Split(strings.Trim(path, "/"), "/")
	if len(p) != len(u) {
		return nil
	}
	params := make(map[string]string)
	for i := range p {
		if strings.HasPrefix(p[i], "{") && strings.HasSuffix(p[i], "}") {
			key := p[i][1 : len(p[i])-1] // remove {}
			params[key] = u[i]
		} else if p[i] != u[i] {
			continue
		}
	}
	return params
}

func extractParamNames(pattern string) []string {
	parts := strings.Split(strings.Trim(pattern, "/"), "/")
	var names []string
	for _, part := range parts {
		if strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}") {
			names = append(names, part[1:len(part)-1])
		}
	}
	return names
}

func (v *V) getSessionIDFromRequest(r *http.Request) string {
	cookie, err := r.Cookie(v.cfg.SessionCookieName)
	if err != nil {
		return ""
	}
	return cookie.Value
}

func (v *V) getOrCreateSession(sessionID string) *session {
	if sessionID == "" {
		sessionID = genRandID()
	}

	v.sessions.registryMu.RLock()
	if sess, ok := v.sessions.registry[sessionID]; ok {
		v.sessions.registryMu.RUnlock()
		return sess
	}
	v.sessions.registryMu.RUnlock()

	// Create new session
	v.sessions.registryMu.Lock()
	defer v.sessions.registryMu.Unlock()

	// Double-check after acquiring write lock
	if sess, ok := v.sessions.registry[sessionID]; ok {
		return sess
	}

	s := newStore()
	s.pathParams = make(map[string]string)
	sess := &session{
		id:        sessionID,
		store:     s,
		patchChan: make(chan patch, 10),
	}
	v.sessions.registry[sessionID] = sess
	return sess
}

func (v *V) getSession(sessionID string) (*session, error) {
	v.sessions.registryMu.RLock()
	defer v.sessions.registryMu.RUnlock()
	if sess, ok := v.sessions.registry[sessionID]; ok {
		return sess, nil
	}
	return nil, fmt.Errorf("session '%s' not found", sessionID)
}

func (v *V) removeSession(sessionID string) {
	v.sessions.registryMu.Lock()
	defer v.sessions.registryMu.Unlock()
	if sess, ok := v.sessions.registry[sessionID]; ok {
		close(sess.patchChan)
		delete(v.sessions.registry, sessionID)
	}
}

func (v *V) warnFn() func(string, ...any) {
	return func(format string, args ...any) {
		v.logWarn(format, args...)
	}
}
