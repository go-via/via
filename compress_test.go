package via_test

import (
	"bufio"
	"bytes"
	"compress/flate"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// deflate stands in for an app's own adapter (brotli, say): a coding via does
// not ship, plugged in through Encoder.
var deflate = via.Encoder{Token: "deflate", NewWriter: func(w io.Writer) via.Compressor {
	fw, _ := flate.NewWriter(w, flate.DefaultCompression)
	return fw
}}

func decode(t *testing.T, coding string, r io.Reader) string {
	t.Helper()
	var dr io.Reader
	switch coding {
	case "":
		dr = r
	case "gzip":
		zr, err := gzip.NewReader(r)
		require.NoError(t, err)
		dr = zr
	case "deflate":
		dr = flate.NewReader(r)
	default:
		t.Fatalf("unexpected Content-Encoding %q", coding)
	}
	b, err := io.ReadAll(dr)
	require.NoError(t, err)
	return string(b)
}

// getEncoded sends Accept-Encoding itself, which stops http.Transport from
// adding gzip and decoding the body behind the test's back.
func getEncoded(t *testing.T, srv string, method, path, accept string, extra ...string) (*http.Response, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, srv+path, nil)
	require.NoError(t, err)
	req.Header.Set("Accept-Encoding", accept)
	for i := 0; i+1 < len(extra); i += 2 {
		req.Header.Set(extra[i], extra[i+1])
	}
	resp, err := http.DefaultTransport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return resp, b
}

func TestCompression_isOffByDefault(t *testing.T) {
	t.Parallel()
	srv := newCounter(t)
	resp, body := getEncoded(t, srv.URL, http.MethodGet, "/", "gzip, br")
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	assert.Contains(t, string(body), "<h1>")
}

func TestPageCompression_negotiatesInTheServersOrder(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(counter{count: &store{}}, via.WithPageCompression(deflate, via.Gzip(6))))
	tests := []struct {
		accept, want string
	}{
		{"gzip, deflate", "deflate"},
		{"gzip;q=1.0, deflate;q=0.1", "deflate"},
		{"gzip", "gzip"},
		{"deflate;q=0, gzip", "gzip"},
		{"*", "deflate"},
		{"*, deflate;q=0", "gzip"},
		{"identity", ""},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.accept, func(t *testing.T) {
			t.Parallel()
			resp, body := getEncoded(t, srv.URL, http.MethodGet, "/", tt.accept)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			assert.Equal(t, tt.want, resp.Header.Get("Content-Encoding"))
			assert.Contains(t, resp.Header.Values("Vary"), "Accept-Encoding")
			assert.Contains(t, decode(t, tt.want, bytes.NewReader(body)), "<h1>")
		})
	}
}

func TestPageCompression_leavesHEADUnencoded(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(counter{count: &store{}}, via.WithPageCompression()))
	resp, _ := getEncoded(t, srv.URL, http.MethodHead, "/", "gzip")
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
}

func TestPageCompression_errorPageKeepsTheCaughtDetail(t *testing.T) {
	t.Parallel()
	r := via.NewRouter(via.WithErrorPage(errPage), via.WithPageCompression())
	via.Mount(r, "/", errOK{})
	t.Cleanup(r.Close)
	srv := serve(t, r)

	resp, body := getEncoded(t, srv.URL, http.MethodGet, "/nowhere", "gzip")
	require.Equal(t, http.StatusNotFound, resp.StatusCode)
	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
	page := decode(t, "gzip", bytes.NewReader(body))
	assert.Contains(t, page, "<h1>sorry</h1>")
	assert.Contains(t, page, "404 page not found", "the mux's own body, buffered as Detail")
}

func TestPageCompression_datastarClientHasAnETagPerCoding(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(counter{count: &store{}}, via.WithPageCompression()))
	plain, plainBody := getEncoded(t, srv.URL, http.MethodGet, "/_via/datastar.js", "")
	zipped, zippedBody := getEncoded(t, srv.URL, http.MethodGet, "/_via/datastar.js", "gzip")

	require.Equal(t, "gzip", zipped.Header.Get("Content-Encoding"))
	assert.Equal(t, string(plainBody), decode(t, "gzip", bytes.NewReader(zippedBody)))
	assert.NotEqual(t, plain.Header.Get("ETag"), zipped.Header.Get("ETag"))
	assert.Regexp(t, `^"[0-9a-f]+"$`, zipped.Header.Get("ETag"))

	resp, body := getEncoded(t, srv.URL, http.MethodGet, "/_via/datastar.js", "gzip", "If-None-Match", zipped.Header.Get("ETag"))
	assert.Equal(t, http.StatusNotModified, resp.StatusCode)
	assert.Empty(t, body)
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
}

func TestPageCompression_datastarClientFailedPreconditionIsWellFormed(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(counter{count: &store{}}, via.WithPageCompression()))
	resp, body := getEncoded(t, srv.URL, http.MethodGet, "/_via/datastar.js", "gzip", "If-Match", `"stale"`)
	assert.Equal(t, http.StatusPreconditionFailed, resp.StatusCode)
	assert.Equal(t, strconv.Itoa(len(body)), resp.Header.Get("Content-Length"))
}

func TestSSECompression_eachFrameDecodesAsItArrives(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(disposeProbe{disposed: make(chan struct{})}, via.WithSSECompression()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))

	// The stream stays open, so a frame only decodes here if its encoder was
	// flushed with it.
	got := make(chan string, 1)
	go func() {
		zr, err := gzip.NewReader(resp.Body)
		if err != nil {
			return
		}
		sc := bufio.NewScanner(zr)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "event: ") {
				got <- sc.Text()
				return
			}
		}
	}()
	select {
	case line := <-got:
		assert.Equal(t, "event: datastar-patch-signals", line)
	case <-time.After(2 * time.Second):
		t.Fatal("no frame decoded while the stream was open")
	}
}

func TestSSECompression_isIndependentOfPageCompression(t *testing.T) {
	t.Parallel()
	srv := serve(t, via.Handler(disposeProbe{disposed: make(chan struct{})}, via.WithPageCompression()))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Empty(t, resp.Header.Get("Content-Encoding"))
}

func TestSSECompression_connectIsRefusedWhenTheWriterCannotStream(t *testing.T) {
	t.Parallel()
	app := via.Handler(disposeProbe{disposed: make(chan struct{})}, via.WithSSECompression())
	srv := serve(t, http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		d := &deadRW{hdr: w.Header(), code: http.StatusOK}
		app.ServeHTTP(d, req)
		w.WriteHeader(d.code)
	}))
	req, err := http.NewRequest(http.MethodPost, srv.URL+"/_via/sse", nil)
	require.NoError(t, err)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Accept-Encoding", "gzip")
	resp, err := http.DefaultTransport.RoundTrip(req)
	require.NoError(t, err)
	resp.Body.Close()
	assert.Equal(t, http.StatusInternalServerError, resp.StatusCode)
}

func TestCompressionOptions_panicOnMisuse(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		opts []via.Option
	}{
		{"second WithPageCompression", []via.Option{via.WithPageCompression(), via.WithPageCompression()}},
		{"second WithSSECompression", []via.Option{via.WithSSECompression(), via.WithSSECompression()}},
		{"duplicate token", []via.Option{via.WithPageCompression(via.Gzip(1), via.Gzip(9))}},
		{"empty token", []via.Option{via.WithSSECompression(via.Encoder{NewWriter: deflate.NewWriter})}},
		{"nil writer", []via.Option{via.WithPageCompression(via.Encoder{Token: "br"})}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() { via.NewRouter(tt.opts...) })
		})
	}
	assert.Panics(t, func() { via.Gzip(42) })
}
