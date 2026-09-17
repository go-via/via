package via_test

import (
	"bytes"
	"errors"
	"log/slog"
	"net/http"
	"sync"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type initFails struct{}

func (p *initFails) OnInit(*via.Ctx) error { return errors.New("the store is down") }
func (p *initFails) View() h.H             { return h.Div(h.Str("never rendered")) }

// The logger writes on the request goroutine; the socket carries no
// happens-before edge the race detector can see, so the buffer takes a lock.
type lockedBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *lockedBuf) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

func TestWithLogger_writesViaDiagnosticsToTheGivenLogger(t *testing.T) {
	t.Parallel()
	var out lockedBuf
	r := via.NewRouter(via.WithLogger(slog.New(slog.NewTextHandler(&out, nil))))
	via.Mount(r, "/p", initFails{})
	srv := serve(t, r)

	resp, _ := do(t, srv, http.MethodGet, "/p", "")
	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)

	logged := out.String()
	assert.Contains(t, logged, "level=ERROR", "a failed OnInit is an error, not chatter")
	assert.Contains(t, logged, "via: OnInit failed")
	assert.Contains(t, logged, "the store is down")
}

func TestWithLogger_panicsOnNilLogger(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { via.NewRouter(via.WithLogger(nil)) })
}
