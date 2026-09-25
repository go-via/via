package signals_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/snippet/src/signals"
)

var between = regexp.MustCompile(`>\s+<`)

func TestGreetingHTML_matchesTheRender(t *testing.T) {
	t.Parallel()
	status, body := vt.Serve(t, via.Handler(signals.Greeting{})).Get("/")
	require.Equal(t, 200, status)
	shown := between.ReplaceAllString(signals.GreetingHTML, "><")
	assert.Contains(t, body, strings.TrimSpace(shown))
}

func TestInbox_namesEachSignalAsCommented(t *testing.T) {
	t.Parallel()
	status, body := vt.Serve(t, via.Handler(signals.Inbox{})).Get("/")
	require.Equal(t, 200, status)
	for _, name := range []string{`"count":0`, `"_open":false`, `"form_email":""`, `"chat__draft":""`} {
		assert.Contains(t, body, name)
	}
}
