package mw_test

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/mw"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type hijackableRecorder struct {
	http.ResponseWriter
	hijacked bool
}

func (h *hijackableRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h.hijacked = true
	return nil, nil, nil
}

// AccessLog wraps the ResponseWriter in an unexported statusWriter. That
// wrapper must not hide optional interfaces (Hijacker, ReaderFrom, …) the
// underlying writer implements — the classic Go middleware footgun. With an
// Unwrap method, http.ResponseController reaches through to the real writer.
func TestAccessLog_preservesHijackerThroughUnwrap(t *testing.T) {
	t.Parallel()
	app := via.New()
	var hijackErr error
	accessLog := mw.AccessLog(app)
	final := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _, hijackErr = http.NewResponseController(w).Hijack()
	})
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		accessLog(w, r, final)
	})

	rec := &hijackableRecorder{ResponseWriter: httptest.NewRecorder()}
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))

	require.NoError(t, hijackErr, "Hijack must succeed through the statusWriter, not return ErrNotSupported")
	assert.True(t, rec.hijacked, "the wrapped writer's Hijack must be reached via Unwrap")
}
