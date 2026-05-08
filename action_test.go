package via_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
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
