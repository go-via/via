// Package vtbrowser (via test, browser) drives a registered via/v2 handler in a
// real headless Chromium and abstracts away raw chromedp, so a browser test
// reads at the level of the behavior it checks:
//
//	s := vtbrowser.Open(t, via.Register(Counter{}))
//	s.Click("button")
//	s.WaitTextContains("p", "count: 1")
//	s.RequireCleanConsole()
//
// It exists to catch the bug class that passes every httptest yet is dead in a
// real browser — Datastar's data-on:click / data-bind / SSE-morph under the
// strict nonce'd CSP. It is a separate module so chromedp never enters the core
// module's dependency graph. Open skips the test when no browser binary is
// found (VIA_CHROME overrides the path), so the suite stays green on a machine
// without Chromium.
package vtbrowser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// defaultTimeout bounds every individual browser operation. Generous because a
// cold headless-Chrome start on a loaded runner can take several seconds.
const defaultTimeout = 20 * time.Second

// pollInterval is how often the Wait* helpers re-read the DOM.
const pollInterval = 50 * time.Millisecond

var browserNames = []string{"chromium", "chromium-browser", "chrome", "google-chrome", "headless-shell"}

// Session is a live headless-browser tab bound to an httptest server running the
// handler. Every helper fails the test on error; browser and server shutdown are
// registered with t.Cleanup.
type Session struct {
	t      testing.TB
	ctx    context.Context // this tab's context
	srv    *httptest.Server
	browse context.Context // the browser context, for spawning sibling tabs

	mu          sync.Mutex
	consoleErrs []string
}

// Open starts an httptest server for handler, launches headless Chromium,
// navigates to the app root, and returns the bound Session. The test is skipped
// when no Chrome/Chromium binary is found (set VIA_CHROME to point at one).
func Open(t testing.TB, handler http.Handler) *Session {
	t.Helper()
	exe := findBrowser()
	if exe == "" {
		t.Skipf("vtbrowser: no browser binary found (looked for %s; set VIA_CHROME to override)",
			strings.Join(browserNames, ", "))
	}

	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	// no-sandbox: runners commonly run as root, where Chrome's sandbox refuses
	// to start. disable-dev-shm-usage: a small container /dev/shm crashes the
	// renderer.
	alloc, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(exe),
			chromedp.Flag("headless", true),
			chromedp.NoSandbox,
			chromedp.DisableGPU,
			chromedp.Flag("disable-dev-shm-usage", true),
		)...)
	t.Cleanup(cancelAlloc)

	browse, cancelBrowser := chromedp.NewContext(alloc)
	t.Cleanup(cancelBrowser)
	// The browser binds to the context this first Run receives, so it must be
	// the long-lived browser context — a per-op timeout context would kill the
	// browser when it expired.
	if err := chromedp.Run(browse); err != nil {
		t.Fatalf("vtbrowser: start browser %s: %v", exe, err)
	}

	s := &Session{t: t, ctx: browse, srv: srv, browse: browse}
	chromedp.ListenTarget(browse, s.collectConsole)
	s.navigate()
	return s
}

// NewTab opens a second tab in the same browser, pointed at the same server —
// the way to drive multi-user behavior (fan-out, presence) where two live
// connections must coexist.
func (s *Session) NewTab() *Session {
	s.t.Helper()
	ctx, cancel := chromedp.NewContext(s.browse)
	s.t.Cleanup(cancel)
	// Bind the new target to its long-lived context with an untimed Run, exactly
	// as Open does for the first tab. The first Run on a context creates and
	// binds its target; if that first Run were the per-op-timeout navigate
	// below, cancelling that timeout child would tear the tab down the moment
	// navigate returned — and every later op on it would hang.
	if err := chromedp.Run(ctx); err != nil {
		s.t.Fatalf("vtbrowser: open new tab: %v", err)
	}
	n := &Session{t: s.t, ctx: ctx, srv: s.srv, browse: s.browse}
	chromedp.ListenTarget(ctx, n.collectConsole)
	n.navigate()
	return n
}

func (s *Session) navigate() {
	s.t.Helper()
	s.run(fmt.Sprintf("navigate to %s/", s.srv.URL),
		chromedp.Navigate(s.srv.URL+"/"),
		chromedp.WaitReady("body", chromedp.ByQuery),
	)
}

// Click dispatches a real mouse click on the first node matching the CSS
// selector, waiting for it to become visible first.
func (s *Session) Click(selector string) {
	s.t.Helper()
	s.run(fmt.Sprintf("click %q", selector), chromedp.Click(selector, chromedp.ByQuery))
}

// Type focuses the first node matching the selector and sends text as real key
// events, so input/keydown listeners (and Datastar binds) fire as for a human.
func (s *Session) Type(selector, text string) {
	s.t.Helper()
	s.run(fmt.Sprintf("type %q into %q", text, selector),
		chromedp.SendKeys(selector, text, chromedp.ByQuery))
}

// Text returns the trimmed textContent of the first node matching the selector
// (empty string if none).
func (s *Session) Text(selector string) string {
	s.t.Helper()
	return strings.TrimSpace(s.text(selector))
}

// Value returns the value of the first input matching the selector.
func (s *Session) Value(selector string) string {
	s.t.Helper()
	var v string
	s.eval(fmt.Sprintf(`(document.querySelector(%q)||{}).value||""`, selector), &v)
	return v
}

// WaitTextContains polls until the first node matching selector has textContent
// containing want, absorbing SSE patch latency without a fixed sleep.
func (s *Session) WaitTextContains(selector, want string) {
	s.t.Helper()
	s.WaitFor(selector, func(text string) bool { return strings.Contains(text, want) },
		fmt.Sprintf("text to contain %q", want))
}

// WaitValue polls until the first input matching selector has value want.
func (s *Session) WaitValue(selector, want string) {
	s.t.Helper()
	deadline := time.After(defaultTimeout)
	var last string
	for {
		last = s.Value(selector)
		if last == want {
			return
		}
		select {
		case <-deadline:
			s.t.Fatalf("vtbrowser: %q value never became %q within %v; last: %q",
				selector, want, defaultTimeout, last)
			return
		case <-time.After(pollInterval):
		}
	}
}

// WaitFor polls the trimmed textContent of selector until ok reports true,
// failing after defaultTimeout with the last observed text. desc names what was
// awaited, for the failure message.
func (s *Session) WaitFor(selector string, ok func(text string) bool, desc string) {
	s.t.Helper()
	deadline := time.After(defaultTimeout)
	var last string
	for {
		last = strings.TrimSpace(s.text(selector))
		if ok(last) {
			return
		}
		select {
		case <-deadline:
			s.t.Fatalf("vtbrowser: %q never satisfied %s within %v; last text: %q",
				selector, desc, defaultTimeout, last)
			return
		case <-time.After(pollInterval):
		}
	}
}

// Eval runs a JavaScript expression and unmarshals its result into out — the
// escape hatch for assertions the named helpers don't cover.
func (s *Session) Eval(js string, out any) {
	s.t.Helper()
	s.eval(js, out)
}

// WaitEvalTrue polls a JavaScript boolean expression until it evaluates true,
// failing after defaultTimeout. For DOM facts the textContent-based Wait*
// helpers can't express — an attribute's value, an element's display style.
func (s *Session) WaitEvalTrue(js, desc string) {
	s.t.Helper()
	deadline := time.After(defaultTimeout)
	for {
		var ok bool
		s.eval(js, &ok)
		if ok {
			return
		}
		select {
		case <-deadline:
			s.t.Fatalf("vtbrowser: expr never became true (%s) within %v: %s", desc, defaultTimeout, js)
			return
		case <-time.After(pollInterval):
		}
	}
}

// Sleep settles for d. Prefer the Wait* helpers, which poll the DOM and so
// absorb latency without a fixed delay; reach for Sleep only when there is no
// observable signal to wait on — e.g. letting the SSE stream connect before a
// first action, where nothing visible changes on connect.
func (s *Session) Sleep(d time.Duration) {
	s.t.Helper()
	s.run(fmt.Sprintf("sleep %v", d), chromedp.Sleep(d))
}

// ConsoleErrors returns every console.error and uncaught exception the tab has
// produced. A clean DOM with a broken console is not a passing client story.
func (s *Session) ConsoleErrors() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.consoleErrs...)
}

// RequireCleanConsole fails the test if the tab logged any console error or
// threw any uncaught exception.
func (s *Session) RequireCleanConsole() {
	s.t.Helper()
	if errs := s.ConsoleErrors(); len(errs) > 0 {
		s.t.Fatalf("vtbrowser: page logged %d console error(s):\n  %s",
			len(errs), strings.Join(errs, "\n  "))
	}
}

func (s *Session) text(selector string) string {
	var txt string
	s.eval(fmt.Sprintf(`(document.querySelector(%q)||{}).textContent||""`, selector), &txt)
	return txt
}

func (s *Session) eval(js string, out any) {
	s.t.Helper()
	s.run(fmt.Sprintf("evaluate %q", js), chromedp.Evaluate(js, out))
}

// run executes chromedp actions with a per-call timeout so one hung operation
// cannot stall the whole suite.
func (s *Session) run(what string, actions ...chromedp.Action) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(s.ctx, defaultTimeout)
	defer cancel()
	if err := chromedp.Run(ctx, actions...); err != nil {
		s.t.Fatalf("vtbrowser: %s: %v", what, err)
	}
}

// collectConsole runs on chromedp's event goroutine, so it only records under
// the mutex — it must never touch testing.TB.
func (s *Session) collectConsole(ev any) {
	switch e := ev.(type) {
	case *runtime.EventConsoleAPICalled:
		if e.Type != runtime.APITypeError {
			return
		}
		parts := make([]string, 0, len(e.Args))
		for _, arg := range e.Args {
			parts = append(parts, formatRemoteObject(arg))
		}
		s.appendConsoleError("console.error: " + strings.Join(parts, " "))
	case *runtime.EventExceptionThrown:
		s.appendConsoleError("uncaught exception: " + e.ExceptionDetails.Error())
	}
}

func (s *Session) appendConsoleError(msg string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.consoleErrs = append(s.consoleErrs, msg)
}

func formatRemoteObject(obj *runtime.RemoteObject) string {
	switch {
	case obj == nil:
		return "<nil>"
	case len(obj.Value) > 0:
		return string(obj.Value)
	case obj.Description != "":
		return obj.Description
	default:
		return string(obj.Type)
	}
}

func findBrowser() string {
	if p := os.Getenv("VIA_CHROME"); p != "" {
		return p
	}
	for _, name := range browserNames {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	if _, err := os.Stat("/bin/chromium"); err == nil {
		return "/bin/chromium"
	}
	return ""
}
