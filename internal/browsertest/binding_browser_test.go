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

// bindPage drives the three reactive binding helpers off signals that start
// truthy via init tags, so the bindings must take effect on first paint.
type bindPage struct {
	On  via.SignalBool     `via:"on,init=true"`
	Hue via.Signal[string] `via:"hue,init=tomato"`
}

func (p *bindPage) View(ctx *via.CtxR) h.H {
	return h.Main(h.ID("root"),
		h.Button(h.ID("target"), h.Text("x"), p.On.Attr("disabled"), p.On.Class("active")),
		h.Span(h.ID("styled"), p.Hue.Style("color"), h.Text("hue")),
	)
}

// Signal.Attr/Class/Style emit Datastar attribute bindings. vt only sees the
// rendered attribute string, so a wrong attribute syntax (e.g. the hyphen form
// data-attr-disabled, which Datastar silently ignores) renders fine yet does
// nothing. This drives the bindings in a real browser and asserts they ACTUALLY
// apply — the regression guard for the colon-syntax fix.
func TestBrowserSignalAttrClassStyleApply(t *testing.T) {
	exec := chromePath()
	if exec == "" {
		t.Skip("no chromium/chrome binary found; set CHROME_PATH to run the browser test")
	}

	app := via.New(via.WithInsecureCookies())
	via.Mount[bindPage](app, "/")
	srv := vt.Serve(t, app)

	allocCtx, cancelAlloc := chromedp.NewExecAllocator(context.Background(),
		append(chromedp.DefaultExecAllocatorOptions[:],
			chromedp.ExecPath(exec), chromedp.Headless, chromedp.DisableGPU, chromedp.NoSandbox,
		)...)
	defer cancelAlloc()
	ctx, cancel := chromedp.NewContext(allocCtx)
	defer cancel()
	ctx, cancelTO := context.WithTimeout(ctx, 30*time.Second)
	defer cancelTO()

	var disabled, hasActive bool
	var color string
	err := chromedp.Run(ctx,
		chromedp.Navigate(srv.URL+"/"),
		chromedp.WaitVisible("#target", chromedp.ByID),
		// Datastar applies the bindings on ready; wait until the attr binding lands.
		waitJSTrue(`document.getElementById('target').hasAttribute('disabled')`),
		chromedp.Evaluate(`document.getElementById('target').hasAttribute('disabled')`, &disabled),
		chromedp.Evaluate(`document.getElementById('target').classList.contains('active')`, &hasActive),
		chromedp.Evaluate(`getComputedStyle(document.getElementById('styled')).color`, &color),
	)
	if err != nil {
		t.Fatalf("chromedp run: %v", err)
	}
	if !disabled {
		t.Fatal("Signal.Attr(\"disabled\") did not apply — Datastar ignored the binding")
	}
	if !hasActive {
		t.Fatal("Signal.Class(\"active\") did not apply — Datastar ignored the binding")
	}
	// tomato == rgb(255, 99, 71)
	if color != "rgb(255, 99, 71)" {
		t.Fatalf("Signal.Style(\"color\") did not apply: computed color = %q, want tomato", color)
	}
}
