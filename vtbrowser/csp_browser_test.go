package vtbrowser_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/mw"
	"github.com/go-via/via/vtbrowser"
	"github.com/stretchr/testify/assert"
)

func TestBrowser_clickFiresUnderDefaultCSPPolicy(t *testing.T) {
	app := newApp()
	app.Use(mw.CSP())
	via.Mount[counter](app, "/")
	s := vtbrowser.Open(t, app)

	s.WaitText("#hits", "0")
	s.Click("#inc")
	s.WaitText("#hits", "1")

	assert.Empty(t, s.ConsoleErrors(),
		"the recommended CSP must not brick the runtime — a policy "+
			"without 'unsafe-eval' makes Datastar's Function() "+
			"compilation throw EvalError on every handler")
}
