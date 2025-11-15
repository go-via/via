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
		v.registerCtx(c)
		if v.cfg.DevMode {
			v.devModePersist(c)
		}
		headElements := v.documentHeadIncludes
		headElements = append(headElements, h.Meta(h.Data("signals", fmt.Sprintf("{'via-ctx':'%s'}", id))))
		headElements = append(headElements, h.Meta(h.Data("init", `window.addEventListener('beforeunload', (evt) => {
			evt.preventDefault(); evt.returnValue = ''; @get('/_session/close'); return ''; })`)))
		headElements = append(headElements, h.Meta(h.Data("init", "@get('/_sse')")))
		bottomBodyElements := []h.H{c.view()}
		bottomBodyElements = append(bottomBodyElements, v.documentFootIncludes...)
		if v.cfg.DevMode {
			bottomBodyElements = append(bottomBodyElements, h.Script(h.Type("module"), h.Src("https://cdn.jsdelivr.net/gh/dataSPA/dataSPA-inspector@latest/dataspa-inspector.bundled.js")))
			bottomBodyElements = append(bottomBodyElements, h.Raw("<dataspa-inspector/>"))
		}
		view := h.HTML5(h.HTML5Props{
			Title:     v.cfg.DocumentTitle,
			Head:      headElements,
			Body:      bottomBodyElements,
			HTMLAttrs: []h.H{},
		})
		_ = view.Render(w)
	}))
}

func (v *V) registerCtx(c *Context) {
	v.contextRegistryMutex.Lock()
	defer v.contextRegistryMutex.Unlock()
	if c == nil {
		v.logErr(c, "failed to add nil context to registry")
		return
	}
	v.contextRegistry[c.id] = c
	v.logDebug(c, "new context added to registry")
}

func (v *V) unregisterCtx(id string) {
	v.contextRegistryMutex.Lock()
	defer v.contextRegistryMutex.Unlock()
	if id == "" {
		return
	}
	v.logDebug(nil, "ctx '%s' removed from registry", id)
	delete(v.contextRegistry, id)
}

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
	if v.cfg.DevMode {
		v.devModeRestore()
	}
	v.logInfo(nil, "via started at [%s]", v.cfg.ServerAddress)
	log.Fatalf("[fatal] %v", http.ListenAndServe(v.cfg.ServerAddress, v.mux))
}

func (v *V) devModePersist(c *Context) {
	p := filepath.Join(".via", "devmode", "ctx.json")
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		log.Fatalf("failed to create directory for devmode files: %v", err)
	}

	// load persisted list from file, or empty list if file not found
	file, err := os.Open(p)
	ctxRegMap := make(map[string]string)
	if err == nil {
		json.NewDecoder(file).Decode(&ctxRegMap)
	}
	file.Close()

	// add ctx to persisted list
	if _, ok := ctxRegMap[c.id]; !ok {
		ctxRegMap[c.id] = c.route
	}

	// write persisted list to file
	file, err = os.Create(p)
	if err != nil {
		v.logErr(c, "devmode failed to percist ctx: %v", err)

	}
	defer file.Close()

	encoder := json.NewEncoder(file)
	if err := encoder.Encode(ctxRegMap); err != nil {
		v.logErr(c, "devmode failed to persist ctx")
	}
	v.logDebug(c, "devmode persisted ctx to file")
}

func (v *V) devModeRestore() {
	p := filepath.Join(".via", "devmode", "ctx.json")
	file, err := os.Open(p)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		v.logErr(nil, "devmode could not restore ctx from file: %v", err)
		return
	}
	defer file.Close()
	var ctxRegMap map[string]string
	if err := json.NewDecoder(file).Decode(&ctxRegMap); err != nil {
		v.logWarn(nil, "devmode could not restore ctx from file: %v", err)
		return
	}
	for ctxID, pageRoute := range ctxRegMap {
		pageInitFn, ok := v.devModePageInitFnMap[pageRoute]
		if !ok {
			v.logWarn(nil, "devmode could not restore ctx from file: page init fn for route '%s' not found", pageRoute)
			continue
		}

		c := newContext(ctxID, pageRoute, v)
		pageInitFn(c)
		v.registerCtx(c)
	}
	v.logDebug(nil, "devmode restored ctx registry")
	os.Remove(p)
}

type patchType int

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
		c, err := v.getCtx(cID)
		if err != nil {
			v.logErr(nil, "sse stream failed to start: %v", err)
			return
		}

		sse := datastar.NewSSE(w, r, datastar.WithCompression(datastar.WithBrotli(datastar.WithBrotliLevel(5))))

		v.logDebug(c, "SSE connection established")

		go func() {
			if v.cfg.DevMode {
				c.Sync()
			} else {
				c.SyncSignals()
			}
		}()

		for {
			select {
			case <-sse.Context().Done():
				v.logDebug(c, "SSE context done, exiting handler loop")
				return
			case patch, ok := <-c.patchChan:
				if !ok {
					v.logDebug(c, "patchChan closed, exiting handler loop")
					return
				}
				switch patch.typ {
				case patchTypeElements:
					if err := sse.PatchElements(patch.content); err != nil {
						v.logErr(c, "PatchElements failed: %v", err)
						return
					}
				case patchTypeSignals:
					if err := sse.PatchSignals([]byte(patch.content)); err != nil {
						v.logErr(c, "PatchSignals failed: %v", err)
						return
					}
				case patchTypeScript:
					if err := sse.ExecuteScript(patch.content, datastar.WithExecuteScriptAutoRemove(true)); err != nil {
						v.logErr(c, "ExecuteScript failed: %v", err)
						return
					}
				}
			}
		}
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

		c.injectSignals(sigs)
		actionFn()
	})

	v.mux.HandleFunc("GET /_session/close", func(w http.ResponseWriter, r *http.Request) {
		var sigs map[string]any
		_ = datastar.ReadSignals(r, &sigs)
		cID, _ := sigs["via-ctx"].(string)
		c, err := v.getCtx(cID)
		if err != nil {
			v.logErr(c, "failed to handle session close: %v", err)
			return
		}
		v.logDebug(c, "session close event triggered")
		v.unregisterCtx(c.id)

	})
	return v
}

func genRandID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)[:8]
}
