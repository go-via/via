package via

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
)

// The wrapper's streaming and body-cap passthroughs guard a coupling nothing
// exercises today: the SSE route is skipped from wrapping, so the only way to
// state the contract is against the wrapper itself.

type countingFlusher struct {
	http.ResponseWriter
	flushes int
}

func (c *countingFlusher) Flush() { c.flushes++ }

func TestErrPageWriter_flushesThroughToTheRealWriter(t *testing.T) {
	t.Parallel()
	inner := &countingFlusher{ResponseWriter: httptest.NewRecorder()}
	w := &errPageWriter{ResponseWriter: inner}

	assert.True(t, canFlush(w), "a wrapped writer must still answer as streamable")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("chunk"))
	w.Flush()
	assert.Equal(t, 1, inner.flushes)
}

func TestErrPageWriter_doesNotFlushACaughtResponse(t *testing.T) {
	t.Parallel()
	inner := &countingFlusher{ResponseWriter: httptest.NewRecorder()}
	w := &errPageWriter{ResponseWriter: inner}

	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte("not found"))
	w.Flush()
	assert.Zero(t, inner.flushes,
		"a caught response is still buffered — flushing it would commit the very body finish replaces")
}

func TestUnwrapWriter_reachesTheWriterNetHTTPInstalled(t *testing.T) {
	t.Parallel()
	rec := httptest.NewRecorder()
	assert.Same(t, rec, unwrapWriter(&errPageWriter{ResponseWriter: rec}),
		"http.MaxBytesReader type-asserts an unexported interface on the writer net/http installed")
	assert.Same(t, rec, unwrapWriter(rec))
}

func TestCanFlush_seesThroughWrappersAndRejectsAPlainWriter(t *testing.T) {
	t.Parallel()
	assert.True(t, canFlush(httptest.NewRecorder()))
	assert.False(t, canFlush(&noFlush{}))
	assert.True(t, canFlush(unwrappable{httptest.NewRecorder()}),
		"a middleware wrapper is the common case a raw http.Flusher assertion gets wrong")
	assert.False(t, canFlush(unwrappable{&noFlush{}}))
}

// noFlush is a ResponseWriter and nothing more.
type noFlush struct{ h http.Header }

func (n *noFlush) Header() http.Header {
	if n.h == nil {
		n.h = http.Header{}
	}
	return n.h
}

func (n *noFlush) Write(p []byte) (int, error) { return len(p), nil }

func (n *noFlush) WriteHeader(int) {}

type unwrappable struct{ http.ResponseWriter }

func (u unwrappable) Unwrap() http.ResponseWriter { return u.ResponseWriter }
