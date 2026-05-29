package h_test

import (
	"errors"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var errWrite = errors.New("h_test: write failed")

// failWriter is a plain io.Writer (no WriteString) that dies once the
// cumulative bytes written would exceed limit — modelling a socket or
// file that fails mid-response. Lacking WriteString forces Render down
// the w.Write fallback path.
type failWriter struct {
	limit   int
	written int
}

func (f *failWriter) Write(p []byte) (int, error) {
	if f.written+len(p) > f.limit {
		n := f.limit - f.written
		if n < 0 {
			n = 0
		}
		f.written = f.limit
		return n, errWrite
	}
	f.written += len(p)
	return len(p), nil
}

// failStringWriter adds WriteString so the same failure also exercises
// Render's WriteString fast path.
type failStringWriter struct{ failWriter }

func (f *failStringWriter) WriteString(s string) (int, error) { return f.Write([]byte(s)) }

// bytesOnlyWriter is a plain io.Writer with no WriteString method, used
// to confirm the fallback path produces identical bytes.
type bytesOnlyWriter struct{ b []byte }

func (w *bytesOnlyWriter) Write(p []byte) (int, error) { w.b = append(w.b, p...); return len(p), nil }

func (w *bytesOnlyWriter) String() string { return string(w.b) }

// assertErrorAtEveryTruncation renders node to a writer that dies at each
// possible byte offset and asserts the error always surfaces — both via
// the plain w.Write path and the WriteString fast path. Truncating at
// every offset exercises every per-write error return inside the node's
// (and its children's) Render methods.
func assertErrorAtEveryTruncation(t *testing.T, node h.H) {
	t.Helper()
	full := r(t, node)
	require.NotEmpty(t, full, "node must render something for truncation to be meaningful")
	for k := 0; k < len(full); k++ {
		assert.ErrorIs(t, node.Render(&failWriter{limit: k}), errWrite,
			"plain writer truncated at byte %d must surface the error", k)
		assert.ErrorIs(t, node.Render(&failStringWriter{failWriter{limit: k}}), errWrite,
			"WriteString writer truncated at byte %d must surface the error", k)
	}
}

func TestRender_propagatesWriterErrorAcrossNodeTypes(t *testing.T) {
	t.Parallel()

	span := func(s string) h.H { return h.Span(h.Text(s)) }
	tests := []struct {
		name string
		node h.H
	}{
		{"element with attribute and escaped text", h.Div(h.Class("box"), h.Text("a<b"))},
		{"void element with boolean attribute", h.Input(h.Attr("required"))},
		{"data attribute", h.Div(h.Data("x", "y"))},
		{"raw node content", h.Div(h.Raw("<b>x</b>"))},
		{"group rendered directly", h.Fragment(span("a"), span("b"))},
		{"group nested in element content", h.Div(h.Fragment(span("a"), span("b")))},
		{"Each-produced group in content", h.Div(h.Each([]string{"a", "b"}, span))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assertErrorAtEveryTruncation(t, tt.node)
		})
	}
}

func TestElement_rendersToWriterLackingWriteString(t *testing.T) {
	t.Parallel()

	// Most writers (bytes.Buffer, strings.Builder) implement WriteString;
	// a plain io.Writer must still produce byte-identical output — including
	// escaping — through the w.Write fallback.
	node := h.Div(h.Class("box"), h.Text("a<b"))
	var plain bytesOnlyWriter
	require.NoError(t, node.Render(&plain))
	assert.Equal(t, r(t, node), plain.String(),
		"the w.Write fallback must match the WriteString fast path")
}
