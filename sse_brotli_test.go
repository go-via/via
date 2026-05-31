package via_test

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"regexp"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type brotliProbePage struct{}

func (p *brotliProbePage) View(ctx *via.CtxR) h.H { return h.Div(h.Text("hi")) }

var brotliTabRE = regexp.MustCompile(`&#34;via_tab&#34;:&#34;([^"&]+)&#34;`)

// When the client negotiates brotli, datastar sets Content-Encoding: br and
// routes writes through a compressing writer. Any RAW write to the underlying
// ResponseWriter (e.g. the handshake comment) injects uncompressed bytes that
// corrupt the stream for a real br browser. Assert the served stream carries
// no raw bytes ahead of the compressed payload.
func TestSSE_brotliHandshakeIsCompressionSafe(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[brotliProbePage](app, "/")
	defer server.Close()

	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	resp, err := c.Get(server.URL + "/")
	require.NoError(t, err)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	m := brotliTabRE.FindStringSubmatch(string(body))
	require.Len(t, m, 2, "tab id in page")
	tabID := m[1]

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	sseURL := server.URL + "/_sse?datastar=" + url.QueryEscape(`{"via_tab":"`+tabID+`"}`)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, sseURL, nil)
	req.Header.Set("Accept-Encoding", "br") // force brotli negotiation
	sresp, err := (&http.Client{Jar: jar}).Do(req)
	require.NoError(t, err)
	defer sresp.Body.Close()

	require.Equal(t, "br", sresp.Header.Get("Content-Encoding"),
		"precondition: datastar must negotiate brotli for this client")

	buf := make([]byte, 64)
	n, _ := sresp.Body.Read(buf)
	raw := string(buf[:n])
	// A valid brotli stream never begins with this ASCII comment; if it does,
	// a raw write bypassed the compressor and the browser's decode breaks.
	assert.NotContains(t, raw, ": ready",
		"raw ': ready' in a Content-Encoding: br stream corrupts real browsers")
}
