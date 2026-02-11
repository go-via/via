package vtest

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	defaultHandler   http.Handler
	defaultHandlerMu sync.RWMutex
)

// SetHandler sets the default handler for Visit.
func SetHandler(handler http.Handler) {
	defaultHandlerMu.Lock()
	defaultHandler = handler
	defaultHandlerMu.Unlock()
}

// Visit creates a new stateful Page by visiting the given path.
func Visit(path string) *Page {
	defaultHandlerMu.RLock()
	handler := defaultHandler
	defaultHandlerMu.RUnlock()

	if handler == nil {
		panic("vtest: no handler set, call vtest.SetHandler first")
	}
	return visitWithHandler(handler, path)
}

func visitWithHandler(handler http.Handler, path string) *Page {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	html := w.Body.String()
	sessionID := extractSessionID(html)
	signals := extractSignals(html)

	// Establish SSE connection
	sse := sseConnect(handler, sessionID)

	return &Page{
		handler:   handler,
		sessionID: sessionID,
		html:      html,
		sse:       sse,
		signals:   signals,
	}
}

// Page represents a stateful page that maintains session and current HTML.
type Page struct {
	handler   http.Handler
	sessionID string
	html      string
	sse       *SSE
	signals   map[string]string // Track current signal values
}

// Click triggers an action by button text or selector.
func (p *Page) Click(selector string) {
	// Find button by text
	actionURLs := extractActionURLs(p.html)
	buttonTexts := extractButtonTexts(p.html)

	var actionURL string
	for i, text := range buttonTexts {
		if text == selector && i < len(actionURLs) {
			actionURL = actionURLs[i]
			break
		}
	}

	if actionURL == "" {
		return // Silently fail for now
	}

	// Trigger action with current signal values
	signals := map[string]any{"via-c": p.sessionID}
	for k, v := range p.signals {
		if k != "via-c" {
			signals[k] = v
		}
	}
	signalsJSON, _ := json.Marshal(signals)
	req := httptest.NewRequest(http.MethodGet, actionURL+"?datastar="+string(signalsJSON), nil)
	w := httptest.NewRecorder()
	p.handler.ServeHTTP(w, req)

	// Wait for SSE patch
	time.Sleep(100 * time.Millisecond)

	// Update page HTML from SSE
	p.updateFromSSE()
}

// AssertText asserts the page contains the given text.
// Handles both static text and Datastar data-text signals.
func (p *Page) AssertText(t any, text string) {
	tb := t.(testing.TB)
	tb.Helper()

	// Get visible text with data-text signals resolved
	visibleText := p.getVisibleText()

	if !strings.Contains(visibleText, text) {
		tb.Fatalf("expected page to contain %q, html:\n%s", text, p.html)
	}
}

// getVisibleText returns the visible text content with data-text signals resolved
func (p *Page) getVisibleText() string {
	html := p.html

	// Replace data-text elements with their signal values
	// Match: <span data-text="$sig_1"></span> -> replace with signal value
	re := regexp.MustCompile(`<[^>]*data-text=["']\$([^"']+)["'][^>]*></[^>]+>`)
	matches := re.FindAllStringSubmatch(html, -1)

	for _, match := range matches {
		if len(match) >= 2 {
			sigID := match[1]
			if sigVal, ok := p.signals[sigID]; ok {
				// Replace the entire element with just the signal value
				html = strings.Replace(html, match[0], sigVal, 1)
			}
		}
	}

	// Strip remaining HTML tags for text extraction
	html = regexp.MustCompile(`<[^>]+>`).ReplaceAllString(html, "")
	// Collapse whitespace
	html = regexp.MustCompile(`\s+`).ReplaceAllString(html, " ")
	html = strings.TrimSpace(html)

	return html
}

func (p *Page) updateFromSSE() {
	// Read latest SSE patches
	events := p.sse.getLatestEvents()
	if len(events) == 0 {
		return
	}

	// Last event contains the latest HTML
	lastEvent := events[len(events)-1]

	// Extract HTML from SSE data (format varies, for now just use the event data)
	// SSE patches contain the updated HTML fragment
	p.html = lastEvent
}

func extractButtonTexts(html string) []string {
	re := regexp.MustCompile(`<button[^>]*>([^<]+)</button>`)
	matches := re.FindAllStringSubmatch(html, -1)

	var texts []string
	for _, match := range matches {
		if len(match) >= 2 {
			texts = append(texts, match[1])
		}
	}
	return texts
}

func sseConnect(handler http.Handler, sessionID string) *SSE {
	req := httptest.NewRequest(http.MethodGet, "/_sse?datastar=%7B%22via-c%22%3A%22"+sessionID+"%22%7D", nil)
	req.Header.Set("Accept", "text/event-stream")

	w := &syncedResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan bool, 1)

	go func() {
		handler.ServeHTTP(w, req)
		done <- true
	}()

	time.Sleep(50 * time.Millisecond) // Wait for SSE to establish

	return &SSE{
		recorder:  w,
		sessionID: sessionID,
		done:      done,
	}
}

// Fill fills an input field with the given value and triggers signal update.
func (p *Page) Fill(name, value string) {
	// Find the input with the given name and extract its data-bind signal
	re := regexp.MustCompile(`<input[^>]*name=["']` + name + `["'][^>]*data-bind=["']([^"']+)["'][^>]*>`)
	match := re.FindStringSubmatch(p.html)
	if match == nil {
		// Try reverse order (data-bind before name)
		re = regexp.MustCompile(`<input[^>]*data-bind=["']([^"']+)["'][^>]*name=["']` + name + `["'][^>]*>`)
		match = re.FindStringSubmatch(p.html)
	}

	if match != nil && len(match) >= 2 {
		sigID := match[1]
		// Update the signal value in our tracked signals
		p.signals[sigID] = value
	}

	// Note: In a real browser, Datastar would update the signal immediately
	// and actions would see the new value. We track it locally and pass it
	// with actions. No server round-trip is needed for Fill itself.
}

// Close closes the page and its SSE connection.
func (p *Page) Close() {
	if p.sse != nil {
		p.sse.Close()
	}
}

// Tester provides ergonomic testing utilities for Via apps.
type Tester struct {
	handler http.Handler
}

// New creates a new Tester for the given Via HTTPServeMux.
func New(handler http.Handler) *Tester {
	return &Tester{handler: handler}
}

// Response wraps an HTTP response with Via-specific helpers.
type Response struct {
	*httptest.ResponseRecorder
	body      string
	sessionID string
	tester    *Tester
}

// Get performs a GET request to the given path.
func (t *Tester) Get(path string) *Response {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	w := httptest.NewRecorder()
	t.handler.ServeHTTP(w, req)

	body := w.Body.String()
	sessionID := extractSessionID(body)

	return &Response{
		ResponseRecorder: w,
		body:             body,
		sessionID:        sessionID,
		tester:           t,
	}
}

// SessionID returns the Via session ID from the response.
func (r *Response) SessionID() string {
	return r.sessionID
}

// AssertStatus asserts the response status code.
func (r *Response) AssertStatus(t any, expected int) {
	tb := t.(testing.TB)
	tb.Helper()
	if r.Code != expected {
		tb.Fatalf("expected status %d, got %d", expected, r.Code)
	}
}

// AssertContains asserts the response body contains the given text.
func (r *Response) AssertContains(t any, text string) {
	tb := t.(testing.TB)
	tb.Helper()
	if !strings.Contains(r.body, text) {
		tb.Fatalf("expected body to contain %q, body:\n%s", text, r.body)
	}
}

// TriggerAction triggers an action by index (0-based).
func (r *Response) TriggerAction(t any, index int) *Response {
	tb := t.(testing.TB)
	tb.Helper()

	actionURLs := extractActionURLs(r.body)
	if index >= len(actionURLs) {
		tb.Fatalf("action index %d out of range (found %d actions)", index, len(actionURLs))
	}

	actionURL := actionURLs[index]
	req := httptest.NewRequest(http.MethodGet, actionURL+"?datastar=%7B%22via-c%22%3A%22"+r.sessionID+"%22%7D", nil)
	w := httptest.NewRecorder()
	r.tester.handler.ServeHTTP(w, req)

	return &Response{
		ResponseRecorder: w,
		body:             w.Body.String(),
		sessionID:        r.sessionID,
		tester:           r.tester,
	}
}

func extractSessionID(html string) string {
	signals := extractSignals(html)
	return signals["via-c"]
}

func extractSignals(html string) map[string]string {
	re := regexp.MustCompile(`data-signals=["']([^"']+)["']`)
	matches := re.FindStringSubmatch(html)
	if len(matches) < 2 {
		return make(map[string]string)
	}

	signalsStr := strings.ReplaceAll(matches[1], "&#39;", "'")
	signalsStr = strings.ReplaceAll(signalsStr, "'", "\"")

	var signals map[string]any
	if err := json.Unmarshal([]byte(signalsStr), &signals); err != nil {
		return make(map[string]string)
	}

	// Convert all values to strings for consistent storage
	result := make(map[string]string)
	for k, v := range signals {
		result[k] = fmt.Sprint(v)
	}

	return result
}

func extractActionURLs(html string) []string {
	re := regexp.MustCompile(`data-on:click=["']@get\(([^)]+)\)["']`)
	matches := re.FindAllStringSubmatch(html, -1)

	var urls []string
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		url := strings.ReplaceAll(match[1], "&#39;", "")
		url = strings.ReplaceAll(url, "'", "")
		urls = append(urls, url)
	}
	return urls
}

// syncedResponseWriter wraps httptest.ResponseRecorder with synchronized access
type syncedResponseWriter struct {
	*httptest.ResponseRecorder
	mu sync.Mutex
}

func (w *syncedResponseWriter) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseRecorder.Write(b)
}

func (w *syncedResponseWriter) safeBodyString() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.ResponseRecorder.Body.String()
}

// SSE establishes an SSE connection and returns a stream helper.
type SSE struct {
	recorder  *syncedResponseWriter
	sessionID string
	done      chan bool
}

// SSE establishes an SSE connection for the given session.
func (t *Tester) SSE(sessionID string) *SSE {
	req := httptest.NewRequest(http.MethodGet, "/_sse?datastar=%7B%22via-c%22%3A%22"+sessionID+"%22%7D", nil)
	req.Header.Set("Accept", "text/event-stream")

	w := &syncedResponseWriter{ResponseRecorder: httptest.NewRecorder()}
	done := make(chan bool, 1)

	go func() {
		t.handler.ServeHTTP(w, req)
		done <- true
	}()

	time.Sleep(100 * time.Millisecond) // Wait for SSE to establish

	return &SSE{
		recorder:  w,
		sessionID: sessionID,
		done:      done,
	}
}

// WaitForEvents waits for the given number of SSE events and returns them.
func (s *SSE) WaitForEvents(count int) []string {
	time.Sleep(200 * time.Millisecond) // Wait for events

	body := s.recorder.safeBodyString()

	scanner := bufio.NewScanner(strings.NewReader(body))
	var events []string
	for scanner.Scan() {
		line := scanner.Text()
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			events = append(events, data)
		}
	}

	return events
}

func (s *SSE) getLatestEvents() []string {
	body := s.recorder.safeBodyString()

	scanner := bufio.NewScanner(strings.NewReader(body))
	var events []string
	for scanner.Scan() {
		line := scanner.Text()
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			events = append(events, data)
		}
	}
	return events
}

// Close closes the SSE connection.
func (s *SSE) Close() {
	// SSE connection closes when test ends
}
