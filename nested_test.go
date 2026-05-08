package via_test

import (
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

type chartCard struct {
	Title via.Signal[string] `via:"title,init=Hello"`
}

func (c *chartCard) View(ctx *via.Ctx) h.H {
	return h.Section(
		h.H2(h.Text("Chart")),
		c.Title.Text(),
	)
}

type dashboard struct {
	Visitors via.State[int]
	Card     chartCard
}

func (d *dashboard) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.H1(h.Text("Dashboard")),
		d.Card.View(ctx),
	)
}

func TestNested_qualifiesChildSignalKey(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[dashboard](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `&#34;Card.title&#34;:&#34;Hello&#34;`,
		"nested Signal must use parent.field qualified key")
}

func TestNested_childViewRendersInsideParent(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[dashboard](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, "<section><h2>Chart</h2>",
		"child View must render inside parent View output")
}
