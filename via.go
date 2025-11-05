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
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync"

	"github.com/go-via/via/h"
	"github.com/starfederation/datastar-go/datastar"
)

//go:embed datastar.js
var datastarJS []byte

// via is the root application.
// It manages page routing, user sessions, and SSE connections for live updates.
type via struct {
	cfg                  Configuration
	mux                  *http.ServeMux
	contextRegistry      map[string]*Context
	contextRegistryMutex sync.RWMutex
	baseLayout           func(h.HTML5Props) h.H
}

func (v *via) logErr(c *Context, format string, a ...any) {
	cRef := ""
	if c != nil && c.id != "" {
		cRef = fmt.Sprintf("via-ctx=%q ", c.id)
	}
	log.Printf("[error] %smsg=%q", cRef, fmt.Sprintf(format, a...))
}

func (v *via) logWarn(c *Context, format string, a ...any) {
	cRef := ""
	if c != nil && c.id != "" {
		cRef = fmt.Sprintf("via-ctx=%q ", c.id)
	}
	if v.cfg.LogLvl <= LogLevelWarn {
		log.Printf("[warn] %smsg=%q", cRef, fmt.Sprintf(format, a...))
	}
}

func (v *via) logInfo(c *Context, format string, a ...any) {
	cRef := ""
	if c != nil && c.id != "" {
		cRef = fmt.Sprintf("via-ctx=%q ", c.id)
	}
	if v.cfg.LogLvl <= LogLevelInfo {
		log.Printf("[info] %smsg=%q", cRef, fmt.Sprintf(format, a...))
	}
}

func (v *via) logDebug(c *Context, format string, a ...any) {
	cRef := ""
	if c != nil && c.id != "" {
		cRef = fmt.Sprintf("via-ctx=%q ", c.id)
	}
	if v.cfg.LogLvl == LogLevelDebug {
		log.Printf("[debug] %smsg=%q", cRef, fmt.Sprintf(format, a...))
	}
}

// Config overrides the default configuration with the given configuration options.
func (v *via) Config(cfg Configuration) {
	if cfg.LogLvl != v.cfg.LogLvl {
		v.cfg.LogLvl = cfg.LogLvl
	}
	if cfg.DocumentHeadIncludes != nil {
		v.cfg.DocumentHeadIncludes = cfg.DocumentHeadIncludes
	}
	if cfg.DocumentBodyIncludes != nil {
		v.cfg.DocumentBodyIncludes = cfg.DocumentBodyIncludes
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
func (v *via) Page(route string, composeContext func(c *Context)) {
	v.mux.HandleFunc("GET "+route, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "favicon") {
			return
		}
		id := fmt.Sprintf("%s_/%s", route, genRandID())
		c := newContext(id, v)
		v.logDebug(c, "GET %s", route)
		composeContext(c)
		v.registerCtx(c.id, c)
		headElements := v.cfg.DocumentHeadIncludes
		headElements = append(headElements, h.Meta(h.Data("signals", fmt.Sprintf("{'via-ctx':'%s'}", id))))
		headElements = append(headElements, h.Meta(h.Data("init", "@get('/_sse')")))
		bottomBodyElements := []h.H{h.Div(h.ID(c.id), c.view())}
		for _, el := range v.cfg.DocumentBodyIncludes {
			bottomBodyElements = append(bottomBodyElements, el)
		}
		view := h.HTML5(h.HTML5Props{
			Title: v.cfg.DocumentTitle,
			Head:  headElements,
			Body:  bottomBodyElements,
		})
		_ = view.Render(w)
	}))
}

func (v *via) registerCtx(id string, c *Context) {
	v.contextRegistryMutex.Lock()
	defer v.contextRegistryMutex.Unlock()
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

func (v *via) getCtx(id string) (*Context, error) {
	if c, ok := v.contextRegistry[id]; ok {
		return c, nil
	}
	return nil, fmt.Errorf("ctx '%s' not found", id)
}

// HandleFunc registers the HTTP handler function for a given pattern. The handler function panics if
// in conflict with another registered handler with the same pattern.
func (v *via) HandleFunc(pattern string, f http.HandlerFunc) {
	v.mux.HandleFunc(pattern, f)
}

// Start starts the Via HTTP server on the given address.
func (v *via) Start(addr string) {
	v.logInfo(nil, "via started")
	log.Fatalf("via failed: %v", http.ListenAndServe(addr, v.mux))
}

// New creates a new Via application with default configuration.
func New() *via {
	mux := http.NewServeMux()
	app := &via{
		mux:             mux,
		contextRegistry: make(map[string]*Context),
		cfg: Configuration{
			LogLvl:               LogLevelDebug,
			DocumentTitle:        "Via Application",
			DocumentHeadIncludes: make([]h.H, 0),
			DocumentBodyIncludes: make([]h.H, 0),
		},
	}

	app.mux.HandleFunc("GET /_datastar.js", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/javascript")
		_, _ = w.Write(datastarJS)
	})

	app.mux.HandleFunc("GET /_sse", func(w http.ResponseWriter, r *http.Request) {
		var sigs map[string]any
		_ = datastar.ReadSignals(r, &sigs)
		cID, _ := sigs["via-ctx"].(string)
		c, err := app.getCtx(cID)
		if err != nil {
			app.logErr(nil, "failed to render page: %v", err)
			return
		}
		c.sse = datastar.NewSSE(w, r)
		app.logDebug(c, "SSE connection established")
		c.SyncSignals()
		<-c.sse.Context().Done()
		c.sse = nil
		app.logDebug(c, "SSE connection closed")
	})
	app.mux.HandleFunc("GET /_action/{id}", func(w http.ResponseWriter, r *http.Request) {
		actionID := r.PathValue("id")
		var sigs map[string]any
		_ = datastar.ReadSignals(r, &sigs)
		cID, _ := sigs["via-ctx"].(string)
		active_ctx_count := 0
		inactive_ctx_count := 0
		for _, c := range app.contextRegistry {
			if c.sse != nil {
				active_ctx_count++
				continue
			}
			inactive_ctx_count++
		}
		app.logDebug(nil, "active_ctx_count=%d inactive_ctx_count=%d", active_ctx_count, inactive_ctx_count)
		c, err := app.getCtx(cID)
		if err != nil {
			app.logErr(nil, "action '%s' failed: %v", actionID, err)
			return
		}
		actionFn, err := c.getActionFn(actionID)
		if err != nil {
			app.logDebug(c, "action '%s' failed: %v", actionID, err)
			return
		}
		// log err if actionFn panics
		defer func() {
			if r := recover(); r != nil {
				app.logErr(c, "action '%s' failed: %v", actionID, r)
			}
		}()
		c.signalsMux.Lock()
		defer c.signalsMux.Unlock()
		app.logDebug(c, "signals=%v", sigs)
		c.injectSignals(sigs)
		actionFn()

	})
	return app
}

func genRandID() string {
	b := make([]byte, 16)
	rand.Read(b)
	return hex.EncodeToString(b)[:8]
}
