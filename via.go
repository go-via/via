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
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/go-via/via/h"
)

//go:embed datastar.js
var datastarJS []byte

// App is the root application.
// It manages page routing, user sessions, and SSE connections for live updates.
type App struct {
	cfg                  config
	mux                  *http.ServeMux
	contextRegistry      map[string]*Context
	contextRegistryMutex sync.RWMutex
	documentHeadIncludes []h.H
	documentFootIncludes []h.H
	documentHTMLAttrs    []h.H
	appStateStore        *sync.Map
}

func (a *App) logFatal(format string, args ...any) {
	log.Printf("[fatal] msg=%q", fmt.Sprintf(format, args...))
}

func (a *App) logErr(c *Context, format string, args ...any) {
	cRef := ""
	if c != nil && c.id != "" {
		cRef = fmt.Sprintf("via-ctx=%q ", c.id)
	}
	log.Printf("[error] %smsg=%q", cRef, fmt.Sprintf(format, args...))
}

func (a *App) logWarn(c *Context, format string, args ...any) {
	if a.cfg.logLevel <= LogWarn {
		cRef := ""
		if c != nil && c.id != "" {
			cRef = fmt.Sprintf("via-ctx=%q ", c.id)
		}
		log.Printf("[warn] %smsg=%q", cRef, fmt.Sprintf(format, args...))
	}
}

func (a *App) logInfo(c *Context, format string, args ...any) {
	if a.cfg.logLevel <= LogInfo {
		cRef := ""
		if c != nil && c.id != "" {
			cRef = fmt.Sprintf("via-ctx=%q ", c.id)
		}
		log.Printf("[info] %smsg=%q", cRef, fmt.Sprintf(format, args...))
	}
}

func (a *App) logDebug(c *Context, format string, args ...any) {
	if a.cfg.logLevel <= LogDebug {
		cRef := ""
		if c != nil && c.id != "" {
			cRef = fmt.Sprintf("via-ctx=%q ", c.id)
		}
		log.Printf("[debug] %smsg=%q", cRef, fmt.Sprintf(format, args...))
	}
}

// AppendToHead appends the given h.H nodes to the head of the base HTML document.
// Useful for including css stylesheets and JS scripts.
func (a *App) AppendToHead(elements ...h.H) {
	for _, el := range elements {
		if el != nil {
			a.documentHeadIncludes = append(a.documentHeadIncludes, el)
		}
	}
}

// AppendAttrToHTML appends attributes to the <html> element of every page.
// Useful for plugins that need to bind data to the root element (e.g. data-theme).
func (a *App) AppendAttrToHTML(attrs ...h.H) {
	for _, attr := range attrs {
		if attr != nil {
			a.documentHTMLAttrs = append(a.documentHTMLAttrs, attr)
		}
	}
}

// AppendToFoot appends the given h.H nodes to the end of the base HTML document body.
// Useful for including JS scripts.
func (a *App) AppendToFoot(elements ...h.H) {
	for _, el := range elements {
		if el != nil {
			a.documentFootIncludes = append(a.documentFootIncludes, el)
		}
	}
}

// Page registers a route and its associated page handler. The handler receives a *Context
// that defines state, UI, signals, and actions.
//
// Example:
//
//	app.Page("/", func(c *via.Context) {
//		c.View(func() h.H {
//			return h.H1(h.Text("Hello, Via!"))
//		})
//	})
func (a *App) Page(route string, initContextFn func(c *Context)) {
	// check for panics
	func() {
		defer func() {
			if err := recover(); err != nil {
				a.logFatal("failed to register page with init func that panics: %v", err)
				panic(err)
			}
		}()
		c := newContext("", "", a)
		initContextFn(c)
		c.view()
	}()

	a.mux.HandleFunc("GET "+route, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.logDebug(nil, "GET %s", r.URL.String())
		if strings.Contains(r.URL.Path, "favicon") ||
			strings.Contains(r.URL.Path, ".well-known") ||
			strings.Contains(r.URL.Path, "js.map") {
			return
		}
		id := fmt.Sprintf("%s_/%s", route, genRandID())
		c := newContext(id, route, a)
		routeParams := extractParams(route, r.URL.Path)
		c.injectRouteParams(routeParams)
		initContextFn(c)
		a.registerCtx(c)
		headElements := []h.H{}
		headElements = append(headElements, a.documentHeadIncludes...)
		initialSigs := c.allSignalValues()
		initialSigs["via-ctx"] = id
		initialSigsJSON, _ := json.Marshal(initialSigs)
		headElements = append(headElements,
			h.Meta(h.Data("signals", string(initialSigsJSON))),
			h.Meta(h.Data("init", "@get('/_sse')")),
			h.Meta(h.Data("init", fmt.Sprintf(`window.addEventListener('beforeunload', (evt) => {
			navigator.sendBeacon('/_sse/close', '%s');});`, c.id))),
		)

		bodyElements := []h.H{c.view()}
		bodyElements = append(bodyElements, a.documentFootIncludes...)
		view := h.HTML5(h.HTML5Props{
			Title:     a.cfg.title,
			Head:      headElements,
			Body:      bodyElements,
			HTMLAttrs: a.documentHTMLAttrs,
		})
		_ = view.Render(w)
	}))
}

func (a *App) registerCtx(c *Context) {
	a.contextRegistryMutex.Lock()
	defer a.contextRegistryMutex.Unlock()
	if c == nil {
		a.logErr(c, "failed to add nil context to registry")
		return
	}
	a.contextRegistry[c.id] = c
	a.logDebug(c, "new context added to registry")
	a.logDebug(nil, "number of sessions in registry: %d", a.currSessionNum())
}

func (a *App) currSessionNum() int {
	return len(a.contextRegistry)
}

func (a *App) unregisterCtx(c *Context) {
	if c.id == "" {
		a.logErr(c, "unregister ctx failed: ctx contains empty id")
		return
	}
	a.contextRegistryMutex.Lock()
	defer a.contextRegistryMutex.Unlock()
	a.logDebug(c, "ctx removed from registry")
	delete(a.contextRegistry, c.id)
	a.logDebug(nil, "number of sessions in registry: %d", a.currSessionNum())
}

func (a *App) getCtx(id string) (*Context, error) {
	a.contextRegistryMutex.RLock()
	defer a.contextRegistryMutex.RUnlock()
	if c, ok := a.contextRegistry[id]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("ctx '%s' not found", id)
}

// Start starts the Via HTTP server on the configured address.
func (a *App) Start() {
	a.logInfo(nil, "via started at [%s]", a.cfg.addr)
	log.Fatalf("[fatal] %v", http.ListenAndServe(a.cfg.addr, a.mux))
}

// HTTPServeMux returns the underlying HTTP request multiplexer to enable user extentions, middleware and
// plugins. It also enables integration with test frameworks like gost-dom/browser for SSE/Datastar testing.
//
// IMPORTANT. The returned *http.ServeMux can only be modified during initialization, before calling via.Start().
// Concurrent handler registration is not safe.
func (a *App) HTTPServeMux() *http.ServeMux {
	return a.mux
}

// New creates a new *App with default configuration.
func New(opts ...Option) *App {
	mux := http.NewServeMux()

	a := &App{
		mux:             mux,
		contextRegistry: make(map[string]*Context),
		appStateStore:   new(sync.Map),
		cfg: config{
			addr:     ":3000",
			logLevel: LogWarn,
			title:    "Via",
		},
	}

	for _, opt := range opts {
		opt(&a.cfg)
	}

	for _, plugin := range a.cfg.plugins {
		if plugin != nil {
			plugin.Register(a)
		}
	}

	a.mux.HandleFunc("GET /_datastar.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write(datastarJS)
	})

	a.mux.HandleFunc("GET /_sse", a.handleSSE)
	a.mux.HandleFunc("POST /_action/{id}", a.handleAction)
	a.mux.HandleFunc("POST /_sse/close", a.handleSSEClose)
	return a
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
