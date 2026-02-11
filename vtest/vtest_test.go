package vtest_test

import (
	"testing"

	"github.com/go-via/via/vtest"
)

func TestVtestGet(t *testing.T) {
	t.Parallel()

	vt := vtest.New(vtest.NewCounterApp())

	resp := vt.Get("/")

	resp.AssertStatus(t, 200)
	resp.AssertContains(t, "Counter")
	resp.AssertContains(t, "Count: 0")
}

func TestVtestSessionID(t *testing.T) {
	t.Parallel()

	vt := vtest.New(vtest.NewCounterApp())

	resp := vt.Get("/")
	sessionID := resp.SessionID()

	if sessionID == "" {
		t.Fatal("expected session ID")
	}
}

func TestVtestTriggerAction(t *testing.T) {
	t.Parallel()

	vt := vtest.New(vtest.NewCounterApp())

	resp := vt.Get("/")
	resp.AssertContains(t, "Count: 0")

	resp = resp.TriggerAction(t, 1)
	resp.AssertStatus(t, 204)
}

func TestVtestSSE(t *testing.T) {
	t.Parallel()

	vt := vtest.New(vtest.NewCounterApp())

	resp := vt.Get("/")
	sessionID := resp.SessionID()

	sse := vt.SSE(sessionID)
	defer sse.Close()

	resp.TriggerAction(t, 1)

	events := sse.WaitForEvents(1)

	if len(events) == 0 {
		t.Fatal("expected SSE events")
	}
}
