package via

import (
	"io"
	"log"
	"net/http"

	"github.com/starfederation/datastar-go/datastar"
)

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

func (a *App) handleSSE(w http.ResponseWriter, r *http.Request) {
	isReconnect := false
	if r.Header.Get("last-event-id") == "via" {
		isReconnect = true
	}
	var sigs map[string]any
	_ = datastar.ReadSignals(r, &sigs)
	cID, _ := sigs["via-ctx"].(string)

	c, err := a.getCtx(cID)
	if err != nil {
		a.logErr(nil, "sse stream failed to start: %v", err)
		return
	}

	sse := datastar.NewSSE(w, r, datastar.WithCompression(datastar.WithBrotli(datastar.WithBrotliLevel(5))))

	// use last-event-id to tell if request is a sse reconnect
	sse.Send(datastar.EventTypePatchElements, []string{}, datastar.WithSSEEventId("via"))

	a.logDebug(c, "SSE connection established")

	go func() {
		if isReconnect {
			c.Sync()
			return
		}
		c.SyncSignals()
	}()

	for {
		select {
		case <-sse.Context().Done():
			a.logDebug(c, "SSE connection ended")
			return
		case patch, ok := <-c.patchChan:
			if !ok {
				continue
			}
			switch patch.typ {
			case patchTypeElements:
				if err := sse.PatchElements(patch.content); err != nil {
					if sse.Context().Err() == nil {
						a.logErr(c, "PatchElements failed: %v", err)
					}
				}
			case patchTypeSignals:
				if err := sse.PatchSignals([]byte(patch.content)); err != nil {
					if sse.Context().Err() == nil {
						a.logErr(c, "PatchSignals failed: %v", err)
					}
				}
			case patchTypeScript:
				if err := sse.ExecuteScript(patch.content, datastar.WithExecuteScriptAutoRemove(true)); err != nil {
					if sse.Context().Err() == nil {
						a.logErr(c, "ExecuteScript failed: %v", err)
					}
				}
			}
		}
	}
}

func (a *App) handleAction(w http.ResponseWriter, r *http.Request) {
	actionID := r.PathValue("id")
	var sigs map[string]any
	_ = datastar.ReadSignals(r, &sigs)
	cID, _ := sigs["via-ctx"].(string)
	c, err := a.getCtx(cID)
	if err != nil {
		a.logErr(nil, "action '%s' failed: %v", actionID, err)
		return
	}
	actionFn, err := c.getActionFn(actionID)
	if err != nil {
		a.logDebug(c, "action '%s' failed: %v", actionID, err)
		return
	}
	defer func() {
		if r := recover(); r != nil {
			a.logErr(c, "action '%s' failed: %v", actionID, r)
		}
	}()

	c.injectSignals(sigs)
	actionFn()
	c.autoSync()
}

func (a *App) handleSSEClose(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		log.Printf("Error reading body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	defer r.Body.Close()
	cID := string(body)
	c, err := a.getCtx(cID)
	if err != nil {
		a.logErr(c, "failed to handle session close: %v", err)
		return
	}
	a.logDebug(c, "session close event triggered")
	a.unregisterCtx(c)
}
