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

// session represents internal user session with persistent state
type session struct {
	id        string
	store     *store
	patchChan chan patch
	c         *Composition
}

// V is the root application.
// It manages page routing, user sessions, and SSE connections for live updates.
type V struct {
	cfg                      Options
	mux                      *http.ServeMux
	compositionRegistry      map[string]*Composition
	compositionRegistryMutex sync.RWMutex
	sessionRegistry          map[string]*session
	sessionRegistryMutex     sync.RWMutex
	documentHeadIncludes     []h.H
	documentFootIncludes     []h.H
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
	if v.cfg.LogLvl == LogLevelDebug {
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
				plugin(v)
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
	c := &Composition{id: genRandID(), route: route}
	composeFn(c)
	if c.viewFn == nil {
		panic("page " + route + " has no view")
	}
	v.compositionRegistryMutex.Lock()
	v.compositionRegistry[c.id] = c
	v.compositionRegistryMutex.Unlock()
	v.mux.HandleFunc("GET "+route, v.newPageHTTPHandler(route, c.id, c))

	// check for panics
	// func() {
	// 	defer func() {
	// 		if err := recover(); err != nil {
	// 			v.logFatal("failed to register page with init func that panics: %v", err)
	// 			panic(err)
	// 		}
	// 	}()
	// 	c := newContext("", "", v)
	// 	initContextFn(c)
	// 	c.view()
	// 	c.stopAllRoutines()
	// }()

	// save page init function allows devmode to restore persisted ctx later
	// if v.cfg.DevMode {
	// 	v.devModePageInitFnMap[route] = initContextFn
	// }
	// v.mux.HandleFunc("GET "+route, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
	// 	v.logDebug( "GET %s", r.URL.String())
	// 	if strings.Contains(r.URL.Path, "favicon") ||
	// 		strings.Contains(r.URL.Path, ".well-known") ||
	// 		strings.Contains(r.URL.Path, "js.map") {
	// 		return
	// 	}
	// 	id := fmt.Sprintf("%s_/%s", route, genRandID())
	// 	c := newContext(id, route, v)
	// 	routeParams := extractParams(route, r.URL.Path)
	// 	c.injectRouteParams(routeParams)
	// 	initContextFn(c)
	// 	v.registerCtx(c)
	// 	if v.cfg.DevMode {
	// 		v.devModePersist(c)
	// 	}
	// 	headElements := []h.H{}
	// 	headElements = append(headElements, v.documentHeadIncludes...)
	// 	headElements = append(headElements,
	// 		h.Meta(h.Data("signals", fmt.Sprintf("{'via-ctx':'%s'}", id))),
	// 		h.Meta(h.Data("init", "@get('/_sse')")),
	// 		h.Meta(h.Data("init", fmt.Sprintf(`window.addEventListener('beforeunload', (evt) => {
	// 		navigator.sendBeacon('/_session/close', '%s');});`, c.id))),
	// 	)
	//
	// 	bodyElements := []h.H{c.view()}
	// 	bodyElements = append(bodyElements, v.documentFootIncludes...)
	// 	if v.cfg.DevMode {
	// 		bodyElements = append(bodyElements, h.Script(h.Type("module"),
	// 			h.Src("https://cdn.jsdelivr.net/gh/dataSPA/dataSPA-inspector@latest/dataspa-inspector.bundled.js")))
	// 		bodyElements = append(bodyElements, h.Raw("<dataspa-inspector/>"))
	// 	}
	// 	view := h.HTML5(h.HTML5Props{
	// 		Title:     v.cfg.DocumentTitle,
	// 		Head:      headElements,
	// 		Body:      bodyElements,
	// 		HTMLAttrs: []h.H{},
	// 	})
	// 	_ = view.Render(w)
	// }))
}

func (v *V) newPageHTTPHandler(route string, cID string, c *Composition) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")

		// Create or get internal session for this page load
		sess := v.getOrCreateSession(cID)
		sess.store.pathParams = extractParams(route, r.URL.Path)
		sess.c = c

		// Create public Session for view (read-only mode)
		sc := &Session{
			s:    sess.store,
			ss:   sess,
			mode: sessionModeView,
			warn: v.warnFn(),
		}

		headElements := []h.H{}
		headElements = append(headElements, v.documentHeadIncludes...)

		// Build initial signals including via-c, path params, and composition signals
		var sb strings.Builder
		fmt.Fprintf(&sb, "{'via-c':'%s'", cID)
		for key, val := range sess.store.pathParams {
			fmt.Fprintf(&sb, ", '%s':'%s'", key, val)
		}
		// Add composition signal initial values
		for _, sig := range c.signals {
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
		initialSignals := sb.String()

		headElements = append(headElements,
			h.Meta(h.Data("signals", initialSignals)),
			h.Meta(h.Data("init", "@get('/_sse', {retry: 'always'})")),
			h.Meta(h.Data("init", fmt.Sprintf(`window.addEventListener('beforeunload', () => { navigator.sendBeacon('/_session/close', '%s'); })`, cID))),
		)
		bodyElements := []h.H{c.viewFn(sc)}
		bodyElements = append(bodyElements, v.documentFootIncludes...)
		page := h.HTML5(h.HTML5Props{
			Title:     v.cfg.DocumentTitle,
			Head:      headElements,
			Body:      bodyElements,
			HTMLAttrs: []h.H{},
		})
		_ = page.Render(w)
	}
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

	sessionID, _ := sigs["via-c"].(string)
	if sessionID != "" {
		session, err := v.getSession(sessionID)
		if err != nil {
			v.logErr("sse stream failed to start: %v", err)
			return
		}

		sse := datastar.NewSSE(w, r, datastar.WithCompression(
			datastar.WithBrotli(datastar.WithBrotliLevel(5)),
			datastar.WithGzip(),
		))
		sse.Send(datastar.EventTypePatchElements, []string{}, datastar.WithSSEEventId("via-sse-reconnect"))

		v.logDebug("SSE connection established for session %s", sessionID)

		for {
			select {
			case <-sse.Context().Done():
				v.logDebug("SSE connection ended for session %s", sessionID)
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

	// Try new C-based system first
	cID, _ := sigs["via-c"].(string)
	if cID != "" {
		v.compositionRegistryMutex.RLock()
		c, ok := v.compositionRegistry[cID]
		v.compositionRegistryMutex.RUnlock()
		if ok {
			if actionFn, exists := c.actions[actionID]; exists {
				// log err if actionFn panics
				defer func() {
					if r := recover(); r != nil {
						v.logErr("action '%s' failed: %v", actionID, r)
					}
				}()

				// Get or create internal session for persistent state
				sess := v.getOrCreateSession(cID)
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

				// Create public Session for action (read-write mode)
				sc := &Session{
					s:    sess.store,
					ss:   sess,
					mode: sessionModeAction,
					warn: v.warnFn(),
				}
				actionFn(sc)
				return
			}
			v.logDebug("action '%s' not found in C", actionID)
			return
		}
		v.logDebug("C with id %q not found", cID)
	}
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
		sessionRegistry:     make(map[string]*session),
		cfg: Options{
			DevMode:       false,
			ServerAddress: ":3000",
			LogLvl:        LogLevelInfo,
			DocumentTitle: "⚡ Via",
		},
	}

	v.mux.HandleFunc("GET /_datastar.js", v.datastarJSHTTPHandler)
	v.mux.HandleFunc("GET /_sse", v.sseHTTPHandler)
	v.mux.HandleFunc("GET /_action/{id}", v.actionHTTPHandler)

	v.mux.HandleFunc("POST /_session/close", v.sessionCloseHTTPHandler)
	return v
}

func genRandID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)[:8]
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

func (v *V) getOrCreateSession(sessionID string) *session {
	v.sessionRegistryMutex.RLock()
	if sess, ok := v.sessionRegistry[sessionID]; ok {
		v.sessionRegistryMutex.RUnlock()
		return sess
	}
	v.sessionRegistryMutex.RUnlock()

	// Create new session
	v.sessionRegistryMutex.Lock()
	defer v.sessionRegistryMutex.Unlock()

	// Double-check after acquiring write lock
	if sess, ok := v.sessionRegistry[sessionID]; ok {
		return sess
	}

	s := newStore()
	s.pathParams = make(map[string]string)
	sess := &session{
		id:        sessionID,
		store:     s,
		patchChan: make(chan patch, 10),
	}
	v.sessionRegistry[sessionID] = sess
	return sess
}

func (v *V) getSession(sessionID string) (*session, error) {
	v.sessionRegistryMutex.RLock()
	defer v.sessionRegistryMutex.RUnlock()
	if sess, ok := v.sessionRegistry[sessionID]; ok {
		return sess, nil
	}
	return nil, fmt.Errorf("session '%s' not found", sessionID)
}

func (v *V) removeSession(sessionID string) {
	v.sessionRegistryMutex.Lock()
	defer v.sessionRegistryMutex.Unlock()
	if sess, ok := v.sessionRegistry[sessionID]; ok {
		close(sess.patchChan)
		delete(v.sessionRegistry, sessionID)
	}
}

func (v *V) warnFn() func(string, ...any) {
	return func(format string, args ...any) {
		v.logWarn(format, args...)
	}
}

// Test helpers
func (v *V) TestGetSession(sessionID string) (*session, error) {
	return v.getSession(sessionID)
}

func (s *session) TestGetPatchChan() <-chan patch {
	return s.patchChan
}

func (p patch) TestContent() string {
	return p.content
}
