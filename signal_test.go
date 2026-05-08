package via_test

import (
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type signalCounter struct {
	Step via.Signal[int] `via:"step,init=1"`
	Name via.Signal[string]
}

func (c *signalCounter) View(ctx *via.Ctx) h.H {
	return h.Div(
		h.Input(h.Type("number"), c.Step.Bind()),
		h.P(c.Step.Text()),
		h.Span(c.Name.Text()),
	)
}

func TestSignal_initFromTagAppearsInPageSignals(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalCounter](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `&#34;step&#34;:1`,
		"signal init=1 must appear as initial value in data-signals meta")
}

func TestSignal_bindRendersAttributeWithKey(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalCounter](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-bind="step"`,
		"Signal.Bind() must render data-bind with the wire key")
}

func TestSignal_textRendersDataTextSpan(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[signalCounter](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `data-text="$step"`)
}

type fieldNameKey struct {
	MyField via.Signal[int]
}

func (c *fieldNameKey) View(ctx *via.Ctx) h.H { return h.Div() }

func TestSignal_keyDefaultsToLowercasedFieldName(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[fieldNameKey](app, "/")
	defer server.Close()

	body := getBody(t, server, "/")
	assert.Contains(t, body, `&#34;myField&#34;:0`)
}

// helpers

func getBody(t *testing.T, server *httptest.Server, path string) string {
	t.Helper()
	resp, err := http.Get(server.URL + path)
	require.NoError(t, err)
	defer resp.Body.Close()
	buf, _ := io.ReadAll(resp.Body)
	return string(buf)
}

func mustBeWellFormedHTML(t *testing.T, body string) {
	t.Helper()
	if !strings.Contains(body, "<html") {
		t.Fatalf("expected <html> in body, got: %s", body)
	}
}

func newCounterPostBody(via_tab string, signals map[string]any) *bytes.Buffer {
	// Datastar reads signals from the JSON body for POST/SSE.
	out := `{"via_tab":"` + via_tab + `"`
	for k, v := range signals {
		out += `,"` + k + `":`
		switch x := v.(type) {
		case string:
			out += `"` + x + `"`
		case int:
			out += strings.TrimSpace(strings.ReplaceAll(formatInt(x), " ", ""))
		}
	}
	out += "}"
	return bytes.NewBufferString(out)
}

func formatInt(i int) string {
	if i == 0 {
		return "0"
	}
	neg := false
	if i < 0 {
		neg = true
		i = -i
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	if neg {
		pos--
		b[pos] = '-'
	}
	return string(b[pos:])
}
