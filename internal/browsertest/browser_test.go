//go:build browser

// Package browsertest holds Via's real-browser end-to-end tests. They are
// gated behind the `browser` build tag so the default `go build/vet/test
// ./...`, golangci-lint, and CI never compile or run them — CI has no
// headless browser. Run them locally with a Chromium present:
//
//	go test -tags browser ./internal/browsertest/... -v
//
// These tests are the anchor for verifying client-side Datastar behaviour
// (signal binding, debounce, key filters, SSE→DOM patching) that the
// server-side `vt` harness cannot reach — `vt` only sees raw SSE frame text,
// never a live DOM with Datastar executing.
package browsertest

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	"github.com/go-via/via/vt"
)

// reactive is a minimal composition exercising the two halves of Via's
// client story that vt cannot verify:
//
//   - Step is a client-bound SignalNum: typing into its <input> updates the
//     mirror span purely in the browser, with no server round-trip.
//   - Hits is server StateTab. Clicking the button fires the Inc action; the
//     server adds Step to Hits and patches the new value back over SSE, which
//     Datastar applies to the DOM.
//
// Asserting both in a real browser proves the SSE→DOM patch path works for
// real, not just as frame text.
type reactive struct {
	Hits via.StateTabNum[int]
	Step via.SignalNum[int] `via:"step,init=1"`
}

func (c *reactive) Inc(ctx *via.Ctx) {
	c.Hits.Op(ctx).Add(c.Step.Read(ctx))
}

func (c *reactive) View(ctx *via.CtxR) h.H {
	return h.Main(
		// Client-only signal mirror — Datastar binds the input to `step`
		// and reflects it into this span without touching the server.
		h.P(h.Text("step:"), h.Span(h.ID("step-mirror"), c.Step.TextSpan())),
		// Server StateTab, patched in over SSE after the action runs.
		h.P(h.Text("hits:"), h.Span(h.ID("hits"), c.Hits.Text(ctx))),
		h.Input(h.ID("step-input"), h.Type("number"), c.Step.Bind()),
		h.Button(h.ID("inc"), h.Text("inc"), on.Click(c.Inc)),
	)
}

// chromePath resolves the Chromium binary: an explicit CHROME_PATH override
// wins, otherwise we probe the common Arch/Debian locations. Returns "" only
// if nothing usable is found, so the test can t.Skip gracefully.
func chromePath() string {
	if p := os.Getenv("CHROME_PATH"); p != "" {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	for _, p := range []string{"/usr/bin/chromium", "/usr/bin/chromium-browser", "/usr/bin/google-chrome", "/usr/bin/google-chrome-stable"} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	return ""
}

func TestBrowserSignalBindAndSSEPatch(t *testing.T) {
	exec := chromePath()
	if exec == "" {
		t.Skip("no chromium/chrome binary found; set CHROME_PATH to run the browser test")
	}

	// Real server. Insecure cookies so the session cookie rides plain
	// http (httptest is http, not https) and the SSE stream authorises.
	app := via.New(via.WithInsecureCookies())
	via.Mount[reactive](app, "/")
	srv := vt.Serve(t, app)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(exec),
			chromedp.Headless,
			chromedp.DisableGPU,
			chromedp.NoSandbox,
		)...)
	defer cancelAlloc()

	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTO := context.WithTimeout(ctx, 30*time.Second)
	defer cancelTO()

	var mirrorAfterType, hitsAfterClick string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		// Wait for the page (and Datastar) to be present.
		chromedp.WaitVisible("#step-input", chromedp.ByID),

		// --- Client-only signal binding: type 4 into the input and the
		// mirror span must reflect it WITHOUT any server round-trip. ---
		chromedp.SetValue("#step-input", "4", chromedp.ByID),
		// Dispatch input so Datastar's data-bind picks up the change.
		chromedp.Evaluate(`document.querySelector('#step-input').dispatchEvent(new Event('input',{bubbles:true}))`, nil),
		waitTextEquals("#step-mirror", "4"),
		chromedp.Text("#step-mirror", &mirrorAfterType, chromedp.ByID, chromedp.NodeVisible),

		// --- Server action + SSE DOM patch: click inc, server adds step(4)
		// to hits and patches the new value over SSE into #hits. ---
		chromedp.Click("#inc", chromedp.ByID),
		waitTextEquals("#hits", "4"),
		chromedp.Text("#hits", &hitsAfterClick, chromedp.ByID, chromedp.NodeVisible),
	)
	if err != nil {
		t.Fatalf("chromedp run: %v", err)
	}

	if mirrorAfterType != "4" {
		t.Fatalf("client signal bind: mirror = %q, want %q", mirrorAfterType, "4")
	}
	if hitsAfterClick != "4" {
		t.Fatalf("server SSE→DOM patch: hits = %q, want %q", hitsAfterClick, "4")
	}
}

// waitTextEquals polls a node's textContent until it equals want or the
// context deadline fires. chromedp has no built-in text-equality wait, and a
// fixed sleep would flake; this polls the live DOM so the SSE patch (which
// arrives asynchronously) is observed deterministically.
func waitTextEquals(sel, want string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		for {
			var got string
			if err := chromedp.Text(sel, &got, chromedp.ByID, chromedp.NodeVisible).Do(ctx); err == nil && got == want {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	})
}
