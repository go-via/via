package via_test

import (
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

type searchPage struct {
	Q     string `query:"q"`
	Page  int    `query:"page"`
	Debug bool   `query:"debug"`
}

func (s *searchPage) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Span(h.Textf("q=%q", s.Q)),
		h.Span(h.Textf("page=%d", s.Page)),
		h.Span(h.Textf("debug=%t", s.Debug)),
	)
}

func TestQuery_decodesIntoTaggedFields(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[searchPage](app, "/search")
	defer server.Close()

	body := getBody(t, server, "/search?"+url.Values{
		"q":     {"hello"},
		"page":  {"3"},
		"debug": {"true"},
	}.Encode())
	assert.Contains(t, body, `q=&#34;hello&#34;`)
	assert.Contains(t, body, "page=3")
	assert.Contains(t, body, "debug=true")
}

func TestQuery_missingFieldsKeepZeroValue(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[searchPage](app, "/search")
	defer server.Close()

	body := getBody(t, server, "/search")
	assert.Contains(t, body, `q=&#34;&#34;`)
	assert.Contains(t, body, "page=0")
	assert.Contains(t, body, "debug=false")
}
