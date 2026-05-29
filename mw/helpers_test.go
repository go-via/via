package mw_test

import (
	"io"
	"net/http/httptest"
	"slices"
	"sync"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

type captureLogger struct {
	mu      sync.Mutex
	records []logRec
}

type logRec struct {
	level via.LogLevel
	msg   string
	kv    []any
}

func (c *captureLogger) Log(level via.LogLevel, msg string, kv ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.records = append(c.records, logRec{level: level, msg: msg, kv: slices.Clone(kv)})
}

func (c *captureLogger) snapshot() []logRec {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.records)
}

// newLoggedApp wires a captureLogger + httptest.Server onto a fresh App,
// applies any extra options, and registers a t.Cleanup so callers don't
// have to track server.Close themselves.
func newLoggedApp(t *testing.T, level via.LogLevel, opts ...via.Option) (*via.App, *httptest.Server, *captureLogger) {
	t.Helper()
	logger := &captureLogger{}
	var server *httptest.Server
	full := append([]via.Option{
		via.WithLogger(logger),
		via.WithLogLevel(level),
		via.WithTestServer(&server),
	}, opts...)
	app := via.New(full...)
	t.Cleanup(func() { server.Close() })
	return app, server, logger
}

func readAll(t *testing.T, r io.Reader) string {
	t.Helper()
	b, _ := io.ReadAll(r)
	return string(b)
}

type accessLogPage struct{}

func (p *accessLogPage) View(ctx *via.CtxR) h.H { return h.Div() }

type ridProbePage struct{}

func (p *ridProbePage) View(*via.CtxR) h.H { return h.Div() }
