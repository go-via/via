package via_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
)

type computedPage struct {
	First via.SignalStr `via:"first,init=Ada"`
	Last  via.SignalStr `via:"last,init=Lovelace"`
}

func (p *computedPage) View(ctx *via.CtxR) h.H {
	return h.Div(
		via.Computed("full", "$first + ' ' + $last"),
		h.Span(via.Effect("console.log($full)")),
	)
}

func TestComputed_definesDerivedSignalFromExpression(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[computedPage](app, "/")

	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-computed-full="$first + &#39; &#39; + $last"`,
		"Computed should emit data-computed-<key> with the derived expression")
}

func TestEffect_runsExpressionReactively(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[computedPage](app, "/")

	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-effect="console.log($full)"`,
		"Effect should emit data-effect with the side-effecting expression")
}
