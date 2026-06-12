// Package vtbrowser (via test, browser) drives a via App in a real
// headless Chrome/Chromium, covering what the DOM-less vt harness
// structurally cannot: Datastar expression evaluation, SSE→DOM morph
// patching, focus preservation, and the reconnect banner lifecycle.
//
//	app := via.New(via.WithInsecureCookies())
//	via.Mount[Counter](app, "/")
//	s := vtbrowser.Open(t, app)
//	s.Click("#inc")
//	s.WaitText("#count", "1")
//	assert.Empty(t, s.ConsoleErrors())
//
// Open skips the test when no browser binary is on PATH, so the suite
// stays green on machines without Chromium; set VIA_BROWSER_REQUIRED=1
// (CI does) to turn that skip into a failure.
package vtbrowser

import (
	"context"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/go-via/via"
	"github.com/stretchr/testify/require"
)

// defaultTimeout bounds every individual browser operation. Generous
// because a cold headless-Chrome start on a loaded CI runner can take
// several seconds before the first navigation completes.
const defaultTimeout = 15 * time.Second

var browserNames = []string{
	"chrome",
	"chromium",
	"chromium-browser",
	"google-chrome",
	"headless-shell",
}

// Session is a live headless-browser tab bound to an httptest server
// running the app. All helpers fail the test on error; cleanup (browser
// and server shutdown) is registered via t.Cleanup.
type Session struct {
	t   testing.TB
	ctx context.Context
	srv *httptest.Server

	mu          sync.Mutex
	consoleErrs []string
}

// Open starts an httptest server for app, launches headless Chromium,
// navigates to the app root, and returns the bound Session.
//
// When no Chrome/Chromium binary is found on PATH the test is skipped —
// unless VIA_BROWSER_REQUIRED=1, which makes the missing browser a hard
// failure so CI cannot silently skip the suite.
func Open(t testing.TB, app *via.App) *Session {
	t.Helper()
	exe := findBrowser()
	if exe == "" {
		msg := fmt.Sprintf(
			"vtbrowser: no browser binary on PATH (looked for %s); install one of them",
			strings.Join(browserNames, ", "))
		if os.Getenv("VIA_BROWSER_REQUIRED") == "1" {
			require.FailNow(t, msg,
				"VIA_BROWSER_REQUIRED=1 forbids skipping browser tests")
		}
		t.Skip(msg + ", or set VIA_BROWSER_REQUIRED=1 to fail instead of skip")
	}

	srv := httptest.NewServer(app)
	t.Cleanup(srv.Close)

	// NoSandbox: CI containers commonly run as root, where Chrome's
	// sandbox refuses to start. disable-dev-shm-usage: container /dev/shm
	// is often too small and crashes the renderer.
	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(exe),
			chromedp.NoSandbox,
			chromedp.DisableGPU,
			chromedp.Flag("disable-dev-shm-usage", true),
		)...)
	t.Cleanup(cancelAlloc)

	ctx, cancel := chromedp.NewContext(allocCtx)
	t.Cleanup(cancel)

	s := &Session{t: t, ctx: ctx, srv: srv}
	chromedp.ListenTarget(ctx, s.collectConsole)
	// The first Run binds the browser process to the context it receives,
	// so it must be the long-lived session context — a timeout-derived one
	// (as s.run uses) would kill the browser the moment it was cancelled.
	require.NoError(t, chromedp.Run(ctx), "vtbrowser: start browser %s", exe)
	s.run(fmt.Sprintf("navigate to %s/", srv.URL),
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitReady("body", chromedp.ByQuery),
	)
	return s
}

// Server exposes the underlying httptest server so tests can simulate
// infrastructure failures — e.g. CloseClientConnections to drop a live
// SSE stream — that no DOM-level helper can express.
func (s *Session) Server() *httptest.Server { return s.srv }

// Click dispatches a real mouse click on the first node matching the
// CSS selector, waiting for it to become visible first.
func (s *Session) Click(selector string) {
	s.t.Helper()
	s.run(fmt.Sprintf("click %q", selector),
		chromedp.Click(selector, chromedp.ByQuery))
}

// Type focuses the first node matching the CSS selector and sends text
// as real key events, so input/keydown listeners (and Datastar binds)
// fire exactly as they would for a human typist.
func (s *Session) Type(selector, text string) {
	s.t.Helper()
	s.run(fmt.Sprintf("type %q into %q", text, selector),
		chromedp.SendKeys(selector, text, chromedp.ByQuery))
}

// WaitText polls until the first node matching the CSS selector has
// trimmed textContent equal to want, failing the test after a timeout
// with the last observed text. Polling (rather than a one-shot read)
// absorbs SSE patch latency without sleeps.
func (s *Session) WaitText(selector, want string) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(s.ctx, defaultTimeout)
	defer cancel()
	js := fmt.Sprintf(
		`(function(){var n=document.querySelector(%q);`+
			`return n ? n.textContent : %q})()`,
		selector, "<vtbrowser: no element matches "+selector+">")
	got := "<vtbrowser: nothing read yet>"
	for {
		err := chromedp.Run(ctx, chromedp.Evaluate(js, &got))
		if err == nil && strings.TrimSpace(got) == want {
			return
		}
		select {
		case <-ctx.Done():
			require.Failf(s.t, "vtbrowser: WaitText timed out",
				"selector %q never showed %q within %v; last text: %q",
				selector, want, defaultTimeout, got)
			return
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// Eval runs a JavaScript expression in the page and unmarshals its
// result into out — the escape hatch for assertions the named helpers
// don't cover (focus, input values, attributes).
func (s *Session) Eval(js string, out any) {
	s.t.Helper()
	s.run(fmt.Sprintf("evaluate %q", js), chromedp.Evaluate(js, out))
}

// SetOffline toggles browser-level network emulation, so tests can
// simulate connectivity loss (failing every in-page fetch, including
// Datastar's SSE reconnect attempts) and its recovery.
func (s *Session) SetOffline(offline bool) {
	s.t.Helper()
	s.run(fmt.Sprintf("set offline=%v", offline),
		network.Enable(),
		network.EmulateNetworkConditions(offline, 0, -1, -1),
	)
}

// ConsoleErrors returns every console.error call and uncaught exception
// the page produced so far. Browser tests assert this is empty — a
// clean DOM with a broken console is not a passing client story.
func (s *Session) ConsoleErrors() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.consoleErrs...)
}

// run executes chromedp actions with a per-call timeout so one hung
// browser operation cannot stall the whole suite.
func (s *Session) run(what string, actions ...chromedp.Action) {
	s.t.Helper()
	ctx, cancel := context.WithTimeout(s.ctx, defaultTimeout)
	defer cancel()
	require.NoError(s.t, chromedp.Run(ctx, actions...), "vtbrowser: %s", what)
}

// collectConsole runs on chromedp's event goroutine, so it only records
// under the mutex — it must never touch testing.TB.
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
	if obj == nil {
		return "<nil>"
	}
	if len(obj.Value) > 0 {
		return string(obj.Value)
	}
	if obj.Description != "" {
		return obj.Description
	}
	return string(obj.Type)
}

func findBrowser() string {
	for _, name := range browserNames {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	return ""
}
