package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
	viatest "github.com/go-via/via/test"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type counterPage struct {
	Hits via.State[int]
	Step via.Signal[int] `via:"step,init=1"`
}

func (c *counterPage) Inc(ctx *via.Ctx) error {
	c.Hits.Set(ctx, c.Hits.Get(ctx)+c.Step.Get(ctx))
	return nil
}

func (c *counterPage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Button(h.Text("+"), on.Click(c.Inc)),
		c.Hits.Text(),
	)
}

func TestAction_methodNameAppearsInOnClickPost(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[counterPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `@post(&#39;/_action/Inc&#39;)`,
		"on.Click(c.Inc) must render @post('/_action/Inc')")
}

func TestAction_unknownMethodReturns404(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[counterPage](app, "/")
	defer server.Close()

	resp, err := http.Post(server.URL+"/_action/Nope", "application/json", strings.NewReader(`{}`))
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestMethodName_resolvesBoundMethod(t *testing.T) {
	t.Parallel()

	c := &counterPage{}
	assert.Equal(t, "Inc", via.MethodName(c.Inc))
}

type erroringActionPage struct{}

func (p *erroringActionPage) Save(ctx *via.Ctx) error {
	return assertSaveErr("validation: email required")
}

func (p *erroringActionPage) View(ctx *via.Ctx) h.H { return h.Div() }

type assertSaveErr string

func (e assertSaveErr) Error() string { return string(e) }

func TestAction_defaultErrorPathAlertsTheBrowser(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[erroringActionPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	frames, cancel := tc.SSE(t)
	defer cancel()
	require.Equal(t, 200, tc.Action("Save").Fire())

	// The default action-error handler queues alert("…"). The script
	// arrives in the SSE stream as a datastar-execute-script event.
	deadline := time.After(2 * time.Second)
	got := strings.Builder{}
	for {
		select {
		case f, ok := <-frames:
			if !ok {
				t.Fatalf("SSE closed early; got %q", got.String())
			}
			got.WriteString(f)
			if strings.Contains(got.String(), `alert("validation: email required")`) {
				return
			}
		case <-deadline:
			t.Fatalf("did not see alert in SSE; got %q", got.String())
		}
	}
}

type customErrPage struct{}

func (p *customErrPage) Save(ctx *via.Ctx) error {
	return assertSaveErr("nope")
}

func (p *customErrPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestAction_WithActionErrorHandler_replacesDefaultAlert(t *testing.T) {
	t.Parallel()

	var seenErr atomic.Pointer[string]
	var server *httptest.Server
	app := via.New(
		via.WithTestServer(&server),
		via.WithActionErrorHandler(func(ctx *via.Ctx, err error) {
			s := err.Error()
			seenErr.Store(&s)
		}),
	)
	via.Mount[customErrPage](app, "/")
	defer server.Close()

	tc := viatest.NewClient(t, server, "/")
	require.Equal(t, 200, tc.Action("Save").Fire())

	got := seenErr.Load()
	require.NotNil(t, got, "WithActionErrorHandler should fire on errored action")
	assert.Equal(t, "nope", *got)
}
