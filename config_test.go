package via_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
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

func logTo(w io.Writer) via.Option { return via.WithLogger(slog.New(slog.NewTextHandler(w, nil))) }

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

func TestNewRouter_warnsAtStartupOnlyWithoutTrustedOrigin(t *testing.T) {
	t.Parallel()
	var open, closed lockedBuf
	via.NewRouter(logTo(&open))
	via.NewRouter(logTo(&closed), via.WithTrustedOrigin("https://example.com"))

	assert.Contains(t, open.String(), "level=WARN")
	assert.Contains(t, open.String(), "accept requests from any origin")
	assert.Contains(t, open.String(), "as the signed-in user", "the warning must state what the open default exposes")
	assert.Contains(t, open.String(), "WithTrustedOrigin")
	assert.Empty(t, closed.String())
}

func TestWithUnsafeEval_addsUnsafeEvalToScriptSrcKeepingNonceAndHashes(t *testing.T) {
	t.Parallel()
	plain, _ := do(t, headSrv(t), http.MethodGet, "/", "")
	evalOK, _ := do(t, headSrv(t, via.WithUnsafeEval()), http.MethodGet, "/", "")
	without := withoutNonce(plain.Header.Get("Content-Security-Policy"))
	with := withoutNonce(evalOK.Header.Get("Content-Security-Policy"))

	assert.NotContains(t, without, "'unsafe-eval'")
	assert.Contains(t, with, "script-src 'self' 'unsafe-eval' <nonce> 'sha256-")
	assert.Equal(t, strings.Replace(without, "script-src 'self'", "script-src 'self' 'unsafe-eval'", 1), with,
		"the option adds one source and changes nothing else")
}

func TestNewRouter_warnsAtStartupOnlyWithUnsafeEval(t *testing.T) {
	t.Parallel()
	var on, off lockedBuf
	origin := via.WithTrustedOrigin("https://example.com")
	via.NewRouter(logTo(&on), origin, via.WithUnsafeEval())
	via.NewRouter(logTo(&off), origin)

	assert.Equal(t, 1, strings.Count(on.String(), "level=WARN"))
	assert.Contains(t, on.String(), "WithUnsafeEval")
	assert.Contains(t, on.String(), "eval, Function or setTimeout(string)", "the warning must name what the CSP stops stopping")
	assert.Empty(t, off.String())
}
