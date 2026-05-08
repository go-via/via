package via_test

import (
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

type langPage struct{}

func (p *langPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestWithLang_setsHTMLLangAttribute(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithLang("en"), via.WithTestServer(&server))
	via.Mount[langPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `<html lang="en">`,
		"WithLang should populate <html lang=…>")
}

func TestWithDescription_setsMetaTag(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithDescription("A reactive Go demo."),
		via.WithTestServer(&server),
	)
	via.Mount[langPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `<meta name="description"`,
		"WithDescription should emit a description meta")
	assert.Contains(t, body, `A reactive Go demo.`)
}

func TestWithLang_emptyByDefault(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[langPage](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.NotContains(t, body, `<html lang="`,
		"unset Lang should not emit a lang attribute")
}
