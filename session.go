package via

import (
	"strconv"

	"github.com/go-via/via/h"
)

type store struct {
	state          map[string]any
	pathParams     map[string]string
	signals        map[string]any
	changedSignals map[string]any
}

type sessionMode uint8

const (
	sessionModeView sessionMode = iota
	sessionModeAction
)

type Session struct {
	s          *store
	ss         *session // internal session (lowercase, defined in via.go)
	mode       sessionMode
	v          *V                 // reference to the app for accessing app/session state
	sessionID  string             // the session ID (from cookie)
	compID     string             // component ID for component actions
	compViewFn func(*Session) h.H // component view function for component actions
	warn       func(string, ...any)
}

func newStore() *store {
	return &store{
		state:          make(map[string]any),
		signals:        make(map[string]any),
		changedSignals: make(map[string]any),
	}
}

func NewSession(v *V) *Session {
	return &Session{
		s:    newStore(),
		mode: sessionModeAction,
		v:    v,
		warn: func(string, ...any) {},
	}
}

func (s *Session) SessionID() string {
	return s.sessionID
}

func (s *Session) PathParam(id string) string {
	if s.s == nil {
		return ""
	}
	if param, ok := s.s.pathParams[id]; ok {
		return param
	}
	return ""
}

func injectSignals(st *store, sigs map[string]any) {
	if sigs == nil {
		return
	}
	// Inject browser-provided signal values into store
	for k, v := range sigs {
		// Skip via-c and path params (they're not signals)
		if k == "via-c" {
			continue
		}
		st.signals[k] = v
	}
}

// Sync re-renders the view and sends the update to the browser via SSE
func (s *Session) Sync() {
	if s.mode == sessionModeView {
		s.warn("Sync() called during view render; no-op")
		return
	}
	if s.ss == nil {
		return
	}

	// If in component context, sync only the component fragment
	if s.compViewFn != nil {
		s.SyncFragment(s.compViewFn(s))
		return
	}

	if s.ss.c == nil {
		return
	}

	// Re-render view with current state
	viewHTML := s.ss.c.viewFn(s)

	// Render to buffer
	buf := make([]byte, 0, 1024)
	writer := &bufferWriter{buf: buf}
	if err := viewHTML.Render(writer); err != nil {
		return
	}

	// Send element patch
	select {
	case s.ss.patchChan <- patch{patchTypeElements, string(writer.buf)}:
	default: // Non-blocking
	}

	// Send signal patches if any signals changed
	if len(s.s.changedSignals) > 0 {
		s.syncSignals()
		// Clear changed signals after sync
		s.s.changedSignals = make(map[string]any)
	}
}

// SyncFragment syncs only a component fragment, not the whole page
func (s *Session) SyncFragment(viewHTML h.H) {
	if s.mode == sessionModeView {
		s.warn("SyncFragment() called during view render; no-op")
		return
	}

	// If we have a component context, wrap with component ID
	if s.compViewFn != nil {
		viewHTML = h.Div(h.ID(s.compID), s.compViewFn(s))
	} else if viewHTML != nil {
		// Use the provided fragment directly (manual call)
		// viewHTML is already set
	} else {
		return // No fragment to sync
	}

	// Render to buffer
	buf := make([]byte, 0, 1024)
	writer := &bufferWriter{buf: buf}
	if err := viewHTML.Render(writer); err != nil {
		return
	}

	// Send element patch
	select {
	case s.ss.patchChan <- patch{patchTypeElements, string(writer.buf)}:
	default: // Non-blocking
	}

	// Send signal patches if any signals changed
	if len(s.s.changedSignals) > 0 {
		s.syncSignals()
		s.s.changedSignals = make(map[string]any)
	}
}

func (s *Session) syncSignals() {
	if len(s.s.changedSignals) == 0 {
		return
	}

	// Build signal patch JSON
	// Format: {"sig_1": 5, "sig_2": "value"}
	signalJSON := "{"
	first := true
	for k, v := range s.s.changedSignals {
		if !first {
			signalJSON += ","
		}
		first = false
		signalJSON += `"` + k + `":`
		switch val := v.(type) {
		case string:
			signalJSON += `"` + val + `"`
		case int:
			signalJSON += strconv.FormatInt(int64(val), 10)
		case int8:
			signalJSON += strconv.FormatInt(int64(val), 10)
		case int16:
			signalJSON += strconv.FormatInt(int64(val), 10)
		case int32:
			signalJSON += strconv.FormatInt(int64(val), 10)
		case int64:
			signalJSON += strconv.FormatInt(val, 10)
		case uint:
			signalJSON += strconv.FormatUint(uint64(val), 10)
		case uint8:
			signalJSON += strconv.FormatUint(uint64(val), 10)
		case uint16:
			signalJSON += strconv.FormatUint(uint64(val), 10)
		case uint32:
			signalJSON += strconv.FormatUint(uint64(val), 10)
		case uint64:
			signalJSON += strconv.FormatUint(val, 10)
		case float32:
			signalJSON += strconv.FormatFloat(float64(val), 'f', -1, 32)
		case float64:
			signalJSON += strconv.FormatFloat(val, 'f', -1, 64)
		case bool:
			signalJSON += strconv.FormatBool(val)
		}
	}
	signalJSON += "}"

	// Send signal patch
	select {
	case s.ss.patchChan <- patch{patchTypeSignals, signalJSON}:
	default:
	}
}

type bufferWriter struct {
	buf []byte
}

func (bw *bufferWriter) Write(p []byte) (n int, err error) {
	bw.buf = append(bw.buf, p...)
	return len(p), nil
}
