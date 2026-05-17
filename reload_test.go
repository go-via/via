package via_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
)

type reloadHelpersPage struct{}

func (p *reloadHelpersPage) DoReload(ctx *via.Ctx) { ctx.Reload() }
func (p *reloadHelpersPage) Notify(ctx *via.Ctx)   { ctx.Toast("saved!") }
func (p *reloadHelpersPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestReload_queuesLocationReloadScript(t *testing.T) {
	t.Parallel()

	c := &reloadHelpersPage{}
	ctx := viatest.NewCtx(t, c)
	c.DoReload(ctx)
	assert.Contains(t, ctx.PendingScripts(), "location.reload()")
}

func TestToast_queuesAlertScript(t *testing.T) {
	t.Parallel()

	c := &reloadHelpersPage{}
	ctx := viatest.NewCtx(t, c)
	c.Notify(ctx)
	assert.Contains(t, ctx.PendingScripts(), `alert("saved!")`)
}

func TestToast_emptyMessageIsNoOp(t *testing.T) {
	t.Parallel()

	c := &reloadHelpersPage{}
	ctx := viatest.NewCtx(t, c)
	ctx.Toast("")
	assert.Empty(t, ctx.PendingScripts())
}

func TestToast_specialCharsSurviveAsValidJSStringLiteral(t *testing.T) {
	t.Parallel()

	c := &reloadHelpersPage{}
	ctx := viatest.NewCtx(t, c)
	// Embedded quotes, newline, and a backslash exercise escape paths
	// where Go's %q diverges from JSON / JS string literal syntax.
	ctx.Toast(`he said "ok\n done"`)
	scripts := ctx.PendingScripts()
	// JSON encodes the inner quote as \" and the newline as \n — both
	// match exactly how a JS engine parses a string literal.
	assert.Contains(t, scripts, `alert("he said \"ok\\n done\"")`,
		"Toast must JSON-encode the message so it parses as a JS string")
}
