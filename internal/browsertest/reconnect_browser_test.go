//go:build browser

package browsertest

import (
	"context"
	"testing"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
)

type plain struct{}

func (p *plain) View(ctx *via.CtxR) h.H { return h.Main(h.ID("root"), h.Text("hi")) }

// The reconnect manager via injects is a Datastar data-init expression. This
// proves three things the server-side suite cannot: (1) Datastar actually
// evaluates the injected IIFE in a real browser (window.__viaRC is set), (2) a
// `retrying` fetch event surfaces the visible banner, and (3) `retries-failed`
// arms the bounded reload (sessionStorage counter), which is the fix for the
// silently-frozen tab after a clean-close deploy.
func TestBrowserReconnectManager(t *testing.T) {
	exec := chromePath()
	if exec == "" {
		t.Skip("no chromium/chrome binary found; set CHROME_PATH to run the browser test")
	}

	app := via.New(via.WithInsecureCookies())
	via.Mount[plain](app, "/")
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

	var installed bool
	var bannerRetry, bannerFailed, reloadCount string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitVisible("#root", chromedp.ByID),

		// (1) Datastar evaluated the injected data-init IIFE.
		waitJSTrue(`window.__viaRC===1`),
		chromedp.Evaluate(`window.__viaRC===1`, &installed),

		// (2) A retrying event shows the visible reconnect banner.
		chromedp.Evaluate(`document.dispatchEvent(new CustomEvent('datastar-fetch',{detail:{type:'retrying'}}))`, nil),
		waitJSTrue(`!!document.getElementById('via-reconnect-banner') && getComputedStyle(document.getElementById('via-reconnect-banner')).display!=='none'`),
		chromedp.Evaluate(`document.getElementById('via-reconnect-banner').textContent`, &bannerRetry),

		// (3) retries-failed arms the bounded reload (counter set, banner updated).
		// Read the counter immediately, before the jittered reload timer fires.
		chromedp.Evaluate(`document.dispatchEvent(new CustomEvent('datastar-fetch',{detail:{type:'retries-failed'}}))`, nil),
		chromedp.Evaluate(`sessionStorage.getItem('__via_rc_reloads')`, &reloadCount),
		chromedp.Evaluate(`document.getElementById('via-reconnect-banner').textContent`, &bannerFailed),
	)
	if err != nil {
		t.Fatalf("chromedp run: %v", err)
	}

	if !installed {
		t.Fatal("reconnect manager did not install — Datastar did not evaluate the injected data-init")
	}
	if bannerRetry != "Reconnecting..." {
		t.Fatalf("retrying banner = %q, want %q", bannerRetry, "Reconnecting...")
	}
	if reloadCount != "1" {
		t.Fatalf("retries-failed must arm a bounded reload: __via_rc_reloads = %q, want %q", reloadCount, "1")
	}
	if bannerFailed == "" || bannerFailed == "Reconnecting..." {
		t.Fatalf("retries-failed banner should escalate; got %q", bannerFailed)
	}
}

func waitJSTrue(expr string) chromedp.Action {
	return chromedp.ActionFunc(func(ctx context.Context) error {
		for {
			var ok bool
			if err := chromedp.Evaluate(expr, &ok).Do(ctx); err == nil && ok {
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
