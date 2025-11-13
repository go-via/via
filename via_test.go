package via

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/andybalholm/brotli"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func TestCompressionBrotli(t *testing.T) {
	v := New()
	v.Page("/", func(c *Context) {
		c.View(func() h.H {
			return h.Div(h.Text(strings.Repeat("Hello Via! ", 200)))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "br")
	w := httptest.NewRecorder()

	v.handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "br", w.Header().Get("Content-Encoding"))

	reader := brotli.NewReader(w.Body)
	decompressed, err := io.ReadAll(reader)
	assert.NoError(t, err)
	assert.Contains(t, string(decompressed), "Hello Via!")
}

func TestCompressionGzip(t *testing.T) {
	v := New()
	v.Page("/", func(c *Context) {
		c.View(func() h.H {
			return h.Div(h.Text(strings.Repeat("Hello Via! ", 200)))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()

	v.handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "gzip", w.Header().Get("Content-Encoding"))

	reader, err := gzip.NewReader(w.Body)
	assert.NoError(t, err)
	decompressed, err := io.ReadAll(reader)
	assert.NoError(t, err)
	assert.Contains(t, string(decompressed), "Hello Via!")
}

func TestCompressionNone(t *testing.T) {
	v := New()
	v.Page("/", func(c *Context) {
		c.View(func() h.H {
			return h.Div(h.Text(strings.Repeat("Hello Via! ", 200)))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	v.handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Content-Encoding"))
	assert.Contains(t, w.Body.String(), "Hello Via!")
}

func TestCompressionMinSize(t *testing.T) {
	v := New()
	v.Page("/", func(c *Context) {
		c.View(func() h.H {
			return h.Div(h.Text("Small"))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "br")
	w := httptest.NewRecorder()

	v.handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Empty(t, w.Header().Get("Content-Encoding"))
}

func TestCompressionLargeResponse(t *testing.T) {
	v := New()
	v.Page("/", func(c *Context) {
		c.View(func() h.H {
			return h.Div(h.Text(strings.Repeat("A", 2000)))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "br")
	w := httptest.NewRecorder()

	v.handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "br", w.Header().Get("Content-Encoding"))
}

func TestCompressionDatastarJS(t *testing.T) {
	v := New()

	req := httptest.NewRequest("GET", "/_datastar.js", nil)
	req.Header.Set("Accept-Encoding", "br")
	w := httptest.NewRecorder()

	v.handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "br", w.Header().Get("Content-Encoding"))
}

func TestCompressionBrotliPreferred(t *testing.T) {
	v := New()
	v.Page("/", func(c *Context) {
		c.View(func() h.H {
			return h.Div(h.Text(strings.Repeat("Hello Via! ", 200)))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "gzip, br")
	w := httptest.NewRecorder()

	v.handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "br", w.Header().Get("Content-Encoding"))
}

func TestCompressionZstdDisabled(t *testing.T) {
	v := New()
	v.Page("/", func(c *Context) {
		c.View(func() h.H {
			return h.Div(h.Text(strings.Repeat("Hello Via! ", 200)))
		})
	})

	req := httptest.NewRequest("GET", "/", nil)
	req.Header.Set("Accept-Encoding", "zstd, br, gzip")
	w := httptest.NewRecorder()

	v.handler.ServeHTTP(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "br", w.Header().Get("Content-Encoding"), "Should use Brotli, not zstd")
	assert.NotEqual(t, "zstd", w.Header().Get("Content-Encoding"))
}
