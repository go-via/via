// Package via provides a reactive web framework for Go.
// It lets you build live, type-safe web interfaces without JavaScript.
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
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/go-via/via/h"
	"github.com/starfederation/datastar-go/datastar"
)

//go:embed datastar.js
var datastarJS []byte

// V is the root application.
// It manages page routing, user sessions, and SSE connections for live updates.
type V struct {
	cfg                  Options
	mux                  *http.ServeMux
	contextRegistry      map[string]*Context
	contextRegistryMutex sync.RWMutex
	documentHeadIncludes []h.H
	documentFootIncludes []h.H
	devModePageInitFnMap map[string]func(*Context)
}

func (v *V) logErr(c *Context, format string, a ...any) {
	cRef := ""
	if c != nil && c.id != "" {
		cRef = fmt.Sprintf("via-ctx=%q ", c.id)
	}
	log.Printf("[error] %smsg=%q", cRef, fmt.Sprintf(format, a...))
}

func (v *V) logWarn(c *Context, format string, a ...any) {
	cRef := ""
	if c != nil && c.id != "" {
		cRef = fmt.Sprintf("via-ctx=%q ", c.id)
	}
	if v.cfg.LogLvl >= LogLevelWarn {
		log.Printf("[warn] %smsg=%q", cRef, fmt.Sprintf(format, a...))
	}
}

func (v *V) logInfo(c *Context, format string, a ...any) {
	cRef := ""
	if c != nil && c.id != "" {
		cRef = fmt.Sprintf("via-ctx=%q ", c.id)
	}
	if v.cfg.LogLvl >= LogLevelInfo {
		log.Printf("[info] %smsg=%q", cRef, fmt.Sprintf(format, a...))
	}
}

func (v *V) logDebug(c *Context, format string, a ...any) {
	cRef := ""
	if c != nil && c.id != "" {
		cRef = fmt.Sprintf("via-ctx=%q ", c.id)
	}
	if v.cfg.LogLvl == LogLevelDebug {
		log.Printf("[debug] %smsg=%q", cRef, fmt.Sprintf(format, a...))
	}
}

// Config overrides the default configuration with the given options.
func (v *V) Config(cfg Options) {
	if cfg.LogLvl != v.cfg.LogLvl {
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

// Page registers a route and its associated page handler.
// The handler receives a *Context to define UI, signals, and actions.
//
// Example:
//
//	v.Page("/", func(c *via.Context) {
//		c.View(func() h.H {
//			return h.H1(h.Text("Hello, Via!"))
//		})
//	})
func (v *V) Page(route string, initContextFn func(c *Context)) {
	if v.cfg.DevMode {
		v.devModePageInitFnMap[route] = initContextFn
	}
	v.mux.HandleFunc("GET "+route, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v.logDebug(nil, "GET %s", route)
		if strings.Contains(r.URL.Path, "favicon") {
			return
		}
		id := fmt.Sprintf("%s_/%s", route, genRandID())
		c := newContext(id, route, v)
		initContextFn(c)
		v.registerCtx(c.id, c)
		if v.cfg.DevMode {
			v.Persist()
		}
		headElements := v.documentHeadIncludes
		headElements = append(headElements, h.Meta(h.Data("signals", fmt.Sprintf("{'via-ctx':'%s'}", id))))
		headElements = append(headElements, h.Meta(h.Data("init", "@get('/_sse')")))
		bottomBodyElements := []h.H{c.view()}
		bottomBodyElements = append(bottomBodyElements, v.documentFootIncludes...)
		view := h.HTML5(h.HTML5Props{
			Title: v.cfg.DocumentTitle,
			Head:  headElements,
			Body:  bottomBodyElements,
		})
		_ = view.Render(w)
	}))
}

func (v *V) registerCtx(id string, c *Context) {
	v.contextRegistryMutex.Lock()
	defer v.contextRegistryMutex.Unlock()
	if c == nil {
		v.logErr(c, "failed to add nil context to registry")
		return
	}
	v.contextRegistry[id] = c
	v.logDebug(c, "new context added to registry")
}

// func (a *App) unregisterCtx(id string) {
// 	if _, ok := a.contextRegistry[id]; ok {
// 		a.contextRegistryMutex.Lock()
// 		defer a.contextRegistryMutex.Unlock()
// 		delete(a.contextRegistry, id)
// 	}
// }

func (v *V) getCtx(id string) (*Context, error) {
	v.contextRegistryMutex.RLock()
	defer v.contextRegistryMutex.RUnlock()
	if c, ok := v.contextRegistry[id]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("ctx '%s' not found", id)
}

// HandleFunc registers the HTTP handler function for a given pattern. The handler function panics if
// in conflict with another registered handler with the same pattern.
func (v *V) HandleFunc(pattern string, f http.HandlerFunc) {
	v.mux.HandleFunc(pattern, f)
}

// Start starts the Via HTTP server on the given address.
func (v *V) Start() {
	v.logInfo(nil, "via started at [%s]", v.cfg.ServerAddress)
	log.Fatalf("[fatal] %v", http.ListenAndServe(v.cfg.ServerAddress, v.mux))
}

func (v *V) Persist() {
	p := filepath.Join(".via", "devmode", "ctx.json")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		log.Fatalf("failed to create directory for devmode files: %v", err)
	}
	file, err := os.Create(p)
	if err != nil {
		log.Printf("devmode: failed to persist ctx: %v", err)
	}
	defer file.Close()
	m := make(map[string]string)
	for ctxID, ctx := range v.contextRegistry {
		m[ctxID] = ctx.route
	}
	encoder := json.NewEncoder(file)
	if err := encoder.Encode(m); err != nil {
		log.Printf("devmode: failed to persist ctx: %s", err)
	}
	log.Printf("devmode persisted ctx registryv")
}

func (v *V) Restore() {
	p := path.Join(".via", "devmode", "ctx.json")
	file, err := os.Open(p)
	if err != nil {
		v.logErr(nil, "devmode failed to restore ctx: %v", err)
		return
	}
	defer file.Close()
	var ctxRegMap map[string]string
	if err := json.NewDecoder(file).Decode(&ctxRegMap); err != nil {
		v.logErr(nil, "devmode failed to restore ctx: %v", err)
		return
	}
	for ctxID, pageRoute := range ctxRegMap {
		pageInitFn, ok := v.devModePageInitFnMap[pageRoute]
		if !ok {
			fmt.Println("devmode failed to restore ctx: page init func of ctx not found")
			return
		}

		c := newContext(ctxID, pageRoute, v)
		pageInitFn(c)
		v.registerCtx(ctxID, c)
		v.logDebug(nil, "devmode restored ctx reg=%v", v.contextRegistry)
	}
}

// New creates a new Via application with default configuration.
func New() *V {
	mux := http.NewServeMux()
	v := &V{
		mux:                  mux,
		contextRegistry:      make(map[string]*Context),
		devModePageInitFnMap: make(map[string]func(*Context)),
		cfg: Options{
			DevMode:       false,
			ServerAddress: ":3000",
			LogLvl:        LogLevelInfo,
			DocumentTitle: "⚡ Via",
		},
	}

	v.mux.HandleFunc("GET /_datastar.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write(datastarJS)
	})

	v.mux.HandleFunc("GET /_sse", func(w http.ResponseWriter, r *http.Request) {
		var sigs map[string]any
		_ = datastar.ReadSignals(r, &sigs)
		cID, _ := sigs["via-ctx"].(string)
		if v.cfg.DevMode && len(v.contextRegistry) == 0 {
			v.Restore()
		}
		c, err := v.getCtx(cID)
		if err != nil {
			v.logErr(nil, "failed to render page: %v", err)
			return
		}
		c.sse = datastar.NewSSE(w, r)
		v.logDebug(c, "SSE connection established")
		if v.cfg.DevMode {
			c.Sync()
		} else {
			c.SyncSignals()
		}
		<-c.sse.Context().Done()
		c.sse = nil
		v.logDebug(c, "SSE connection closed")
	})
	v.mux.HandleFunc("GET /_action/{id}", func(w http.ResponseWriter, r *http.Request) {
		actionID := r.PathValue("id")
		var sigs map[string]any
		_ = datastar.ReadSignals(r, &sigs)
		cID, _ := sigs["via-ctx"].(string)
		c, err := v.getCtx(cID)
		if err != nil {
			v.logErr(nil, "action '%s' failed: %v", actionID, err)
			return
		}
		actionFn, err := c.getActionFn(actionID)
		if err != nil {
			v.logDebug(c, "action '%s' failed: %v", actionID, err)
			return
		}
		// log err if actionFn panics
		defer func() {
			if r := recover(); r != nil {
				v.logErr(c, "action '%s' failed: %v", actionID, r)
			}
		}()
		c.signalsMux.Lock()
		defer c.signalsMux.Unlock()
		v.logDebug(c, "signals=%v", sigs)
		c.injectSignals(sigs)
		actionFn()
	})
	return v
}

func genRandID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)[:8]
}
