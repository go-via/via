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
	"time"

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

func TestWithMaxSSEConn_panicsOnANonPositiveCap(t *testing.T) {
	t.Parallel()
	for _, n := range []int{0, -1} {
		assert.PanicsWithValue(t, "via: WithMaxSSEConn must be positive", func() { via.NewRouter(via.WithMaxSSEConn(n)) })
	}
}

func TestWithPinnedDeadline_panicsOnANonPositiveDeadline(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{0, -time.Second} {
		assert.PanicsWithValue(t, "via: WithPinnedDeadline must be positive", func() { via.NewRouter(via.WithPinnedDeadline(d)) })
	}
}

func TestWithSessionStoreTimeout_panicsOnANonPositiveTimeout(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{0, -time.Second} {
		assert.PanicsWithValue(t, "via: WithSessionStoreTimeout must be positive", func() { via.NewRouter(via.WithSessionStoreTimeout(d)) })
	}
}

func TestWithSessionTTL_panicsOnANonPositiveTTL(t *testing.T) {
	t.Parallel()
	for _, d := range []time.Duration{0, -time.Hour} {
		assert.PanicsWithValue(t, "via: WithSessionTTL must be positive", func() { via.NewRouter(via.WithSessionTTL(d)) })
	}
}

func TestWithSessionCookieName_panicsOnANameNetHTTPWouldDrop(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "my sid", "sid;x", `sid"`, "sid=x", "sid,x", "sïd"} {
		assert.Panics(t, func() { via.NewRouter(via.WithSessionCookieName(name)) }, name)
	}
	for _, name := range []string{"myapp_sid", "via_session_v0_8", "__Host-sid"} {
		assert.NotPanics(t, func() { via.NewRouter(via.WithSessionCookieName(name)) }, name)
	}
}

func TestWithSessionStore_panicsWhenGivenTwice(t *testing.T) {
	t.Parallel()
	assert.PanicsWithValue(t, "via: conflicting WithSessionStore options; pass one store", func() {
		via.NewRouter(via.WithSessionStore(via.NewMemorySessionStore()), via.WithSessionStore(via.NewMemorySessionStore()))
	})
}

func TestWithSessionKey_panicsWhenGivenTwice(t *testing.T) {
	t.Parallel()
	key := []byte("a-test-signing-key-32-bytes-long")
	assert.PanicsWithValue(t, "via: conflicting WithSessionKey options; pass one key", func() {
		via.NewRouter(via.WithSessionKey(key), via.WithSessionKey(key))
	})
}

func TestWithSessionKey_panicsOnAnEmptyKey(t *testing.T) {
	t.Parallel()
	for _, key := range [][]byte{nil, {}} {
		assert.Panics(t, func() { via.NewRouter(via.WithSessionKey(key)) })
	}
}
