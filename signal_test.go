package via_test

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSignal_createsWithInitialValue(t *testing.T) {
	v := via.New()
	var got string
	v.Page("/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "hello")
		cmp.View(func(ctx *via.Ctx) h.H {
			got = sig.Get(ctx)
			return h.Div()
		})
	})
	assert.Equal(t, "hello", got)
}

func TestSignal_getReturnsTypedValue(t *testing.T) {
	v := via.New()
	var got int
	v.Page("/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, 42)
		cmp.View(func(ctx *via.Ctx) h.H {
			got = sig.Get(ctx)
			return h.Div()
		})
	})
	assert.Equal(t, 42, got)
}

func TestSignal_idReturnsNonEmpty(t *testing.T) {
	v := via.New()
	var idA, idB string
	v.Page("/", func(cmp *via.Cmp) {
		a := via.Signal(cmp, "a")
		b := via.Signal(cmp, "b")
		idA = a.ID()
		idB = b.ID()
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	require.NotEmpty(t, idA)
	require.NotEmpty(t, idB)
	assert.NotEqual(t, idA, idB)
}

func TestSignal_sliceSerializesForTransport(t *testing.T) {
	v := via.New()
	var got []string
	v.Page("/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, []string{"a", "b"})
		cmp.View(func(ctx *via.Ctx) h.H {
			got = sig.Get(ctx)
			return h.Div()
		})
	})
	assert.Equal(t, []string{"a", "b"}, got)
}

func TestSignal_bindRendersDataBindAttr(t *testing.T) {
	sig := captureSignal(func(cmp *via.Cmp) signalT { return via.Signal(cmp, "x") })
	out := renderH(t, h.Input(sig.Bind()))
	assert.Contains(t, out, "data-bind")
	assert.Contains(t, out, sig.ID())
}

func TestSignal_textUsesSignalRefSoDatastarCanResolveIt(t *testing.T) {
	sig := captureSignal(func(cmp *via.Cmp) signalT { return via.Signal(cmp, "world") })
	out := renderH(t, h.Div(sig.Text()))
	assert.Contains(t, out, "<span")
	assert.Contains(t, out, "data-text")
	assert.Contains(t, out, "$"+sig.ID(), "data-text must use $ prefix so Datastar resolves the signal")
}

func TestSignal_showUsesSignalRefSoDatastarCanResolveIt(t *testing.T) {
	sig := captureSignal(func(cmp *via.Cmp) signalT { return via.Signal(cmp, true) })
	out := renderH(t, h.Div(sig.Show()))
	assert.Contains(t, out, "data-show")
	assert.Contains(t, out, "$"+sig.ID(), "data-show must use $ prefix so Datastar resolves the signal")
}

func TestSignal_tagPrependsLabel(t *testing.T) {
	sig := captureSignal(func(cmp *via.Cmp) signalT {
		s := via.Signal(cmp, "")
		s.Tag("search")
		return s
	})
	assert.Contains(t, sig.Ref(), "search")
}

func TestSignal_refReturnsDollarID(t *testing.T) {
	sig := captureSignal(func(cmp *via.Cmp) signalT { return via.Signal(cmp, "x") })
	assert.Equal(t, "$"+sig.ID(), sig.Ref())
}

func TestSignal_tagAffectsBindID(t *testing.T) {
	sig := captureSignal(func(cmp *via.Cmp) signalT {
		s := via.Signal(cmp, "")
		s.Tag("myfield")
		return s
	})
	out := renderH(t, h.Input(sig.Bind()))
	assert.Contains(t, out, "myfield")
}

func TestSignal_intGetAfterNumericJSONInjection(t *testing.T) {
	gotCh := make(chan int, 1)
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, 1000)
		act := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- sig.Get(ctx)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Bind(), act.OnChange())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	sigsJSON := fmt.Sprintf(`{"via_tab":%q,%q:500}`, ctxID, sigID)
	resp, err := http.Post(server.URL+"/_action/"+actionID, "application/json", strings.NewReader(sigsJSON))
	require.NoError(t, err)
	resp.Body.Close()

	select {
	case got := <-gotCh:
		assert.Equal(t, 500, got)
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for action")
	}
}

func TestSignal_intInitialValueSerializesAsJSONNumber(t *testing.T) {
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, 1000)
		cmp.View(func(ctx *via.Ctx) h.H { return h.Input(sig.Bind()) })
	})
	body := getPageBody(t, server, "/")
	sigID := extractSignalID(t, body)
	numericEntry := "&#34;" + sigID + "&#34;:1000"
	assert.Contains(t, body, numericEntry, "int signal should be encoded as a JSON number, not a string")
}

func TestSignal_idHasViaPrefix(t *testing.T) {
	sig := captureSignal(func(cmp *via.Cmp) signalT { return via.Signal(cmp, "x") })
	assert.True(t, strings.HasPrefix(sig.ID(), "via_"), "signal ID %q must start with via_", sig.ID())
}

func TestSignal_displayIDHasViaPrefixWhenUntagged(t *testing.T) {
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "hello")
		cmp.View(func(ctx *via.Ctx) h.H { return h.Input(sig.Bind()) })
	})
	body := getPageBody(t, server, "/")
	sigID := extractSignalID(t, body)
	assert.True(t, strings.HasPrefix(sigID, "via_"), "display ID %q in HTML must start with via_", sigID)
}

func TestSignal_displayIDHasViaPrefixWhenTagged(t *testing.T) {
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "hello")
		sig.Tag("search")
		cmp.View(func(ctx *via.Ctx) h.H { return h.Input(sig.Bind()) })
	})
	body := getPageBody(t, server, "/")
	sigID := extractSignalID(t, body)
	assert.True(t, strings.HasPrefix(sigID, "search_via_"), "tagged display ID %q must start with search_via_", sigID)
}

func TestSignal_valuesArePerTab(t *testing.T) {
	t.Parallel()

	gotCh1 := make(chan int, 1)
	gotCh2 := make(chan int, 1)

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Page("/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, 0)
		act := cmp.Action(func(ctx *via.Ctx) error {
			gotCh1 <- sig.Get(ctx)
			return nil
		})
		readAct := cmp.Action(func(ctx *via.Ctx) error {
			gotCh2 <- sig.Get(ctx)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Bind(), act.OnClick(), readAct.OnClick())
		})
	})
	defer server.Close()

	// Tab 1: inject signal = 100
	body1 := getPageBody(t, server, "/")
	ctxID1 := extractCtxID(t, body1)
	actionIDs1 := extractActionIDs(t, body1)
	sigID := extractSignalID(t, body1)

	_, cancel1 := connectSSE(t, server, ctxID1)
	defer cancel1()
	time.Sleep(20 * time.Millisecond)

	postSignal(t, server.URL, ctxID1, actionIDs1[0], sigID, 100)

	select {
	case got := <-gotCh1:
		assert.Equal(t, 100, got, "tab 1 should see its own injected value")
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for tab 1 action")
	}

	// Tab 2: inject signal = 200
	body2 := getPageBody(t, server, "/")
	ctxID2 := extractCtxID(t, body2)
	actionIDs2 := extractActionIDs(t, body2)

	_, cancel2 := connectSSE(t, server, ctxID2)
	defer cancel2()
	time.Sleep(20 * time.Millisecond)

	postSignal(t, server.URL, ctxID2, actionIDs2[1], sigID, 200)

	select {
	case got := <-gotCh2:
		assert.Equal(t, 200, got, "tab 2 should see its own injected value")
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for tab 2 action")
	}

	// Tab 1 again: should still see 100, NOT 200
	postSignal(t, server.URL, ctxID1, actionIDs1[0], sigID, 100)

	select {
	case got := <-gotCh1:
		assert.Equal(t, 100, got, "tab 1 must not see tab 2's signal value")
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for tab 1 re-read")
	}
}

func TestSignal_setValueWritesToCtx(t *testing.T) {
	t.Parallel()

	gotCh := make(chan string, 1)
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "initial")
		setAct := cmp.Action(func(ctx *via.Ctx) error {
			sig.SetValue(ctx, "from-server")
			return nil
		})
		readAct := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- sig.Get(ctx)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(setAct.OnClick(), readAct.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionIDs := extractActionIDs(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionIDs[0])
	// drain signal patch
	readSSEEvent(t, stream, sseTimeout)

	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionIDs[1])

	select {
	case got := <-gotCh:
		assert.Equal(t, "from-server", got, "SetValue must persist in ctx for subsequent actions")
	case <-time.After(sseTimeout):
		t.Fatal("timed out")
	}
}

func TestSignal_coercesJSONFloatToInt64(t *testing.T) {
	t.Parallel()

	gotCh := make(chan int64, 1)
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, int64(0))
		act := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- sig.Get(ctx)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Bind(), act.OnChange())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	postSignal(t, server.URL, ctxID, actionID, sigID, 999)

	select {
	case got := <-gotCh:
		assert.Equal(t, int64(999), got)
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for action")
	}
}

func TestSignal_coercesJSONFloatToFloat32(t *testing.T) {
	t.Parallel()

	gotCh := make(chan float32, 1)
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, float32(0))
		act := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- sig.Get(ctx)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Bind(), act.OnChange())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	postSignal(t, server.URL, ctxID, actionID, sigID, 3.14)

	select {
	case got := <-gotCh:
		assert.InDelta(t, float32(3.14), got, 0.01)
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for action")
	}
}

func TestSignal_coercesJSONFloatToUint(t *testing.T) {
	t.Parallel()

	gotCh := make(chan uint, 1)
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, uint(0))
		act := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- sig.Get(ctx)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Bind(), act.OnChange())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	postSignal(t, server.URL, ctxID, actionID, sigID, 42)

	select {
	case got := <-gotCh:
		assert.Equal(t, uint(42), got)
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for action")
	}
}

func TestSignal_coerceFallsBackWhenTypeDoesNotMatch(t *testing.T) {
	t.Parallel()

	gotCh := make(chan string, 1)
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, "default")
		act := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- sig.Get(ctx)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Bind(), act.OnChange())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	triggerActionWithSignal(t, server.URL, ctxID, actionID, sigID, "updated")

	select {
	case got := <-gotCh:
		assert.Equal(t, "updated", got)
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for action")
	}
}

func TestSignal_structInitialSerializesAsJSON(t *testing.T) {
	t.Parallel()

	type point struct {
		X int `json:"x"`
		Y int `json:"y"`
	}
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, point{X: 1, Y: 2})
		cmp.View(func(ctx *via.Ctx) h.H { return h.Input(sig.Bind()) })
	})
	body := getPageBody(t, server, "/")
	assert.Contains(t, body, "{\\&#34;x\\&#34;:1,\\&#34;y\\&#34;:2}", "struct signal should be JSON-serialized in page HTML")
}

func TestSignal_coercesJSONFloatToInt32(t *testing.T) {
	t.Parallel()

	gotCh := make(chan int32, 1)
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, int32(0))
		act := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- sig.Get(ctx)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Bind(), act.OnChange())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	postSignal(t, server.URL, ctxID, actionID, sigID, -77)

	select {
	case got := <-gotCh:
		assert.Equal(t, int32(-77), got)
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for action")
	}
}

func TestSignal_coercesJSONFloatToUint64(t *testing.T) {
	t.Parallel()

	gotCh := make(chan uint64, 1)
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, uint64(0))
		act := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- sig.Get(ctx)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Bind(), act.OnChange())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	postSignal(t, server.URL, ctxID, actionID, sigID, 12345)

	select {
	case got := <-gotCh:
		assert.Equal(t, uint64(12345), got)
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for action")
	}
}

func TestSignal_coercesJSONFloatToFloat64(t *testing.T) {
	t.Parallel()

	gotCh := make(chan float64, 1)
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, float64(0))
		act := cmp.Action(func(ctx *via.Ctx) error {
			gotCh <- sig.Get(ctx)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(sig.Bind(), act.OnChange())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	postSignal(t, server.URL, ctxID, actionID, sigID, 2.718)

	select {
	case got := <-gotCh:
		assert.InDelta(t, 2.718, got, 0.001)
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for action")
	}
}

func TestSignal_setValueOverwritesExistingValue(t *testing.T) {
	t.Parallel()

	gotCh := make(chan int, 1)
	server := newTestApp(t, "/", func(cmp *via.Cmp) {
		sig := via.Signal(cmp, 0)
		setAct := cmp.Action(func(ctx *via.Ctx) error {
			sig.SetValue(ctx, 10)
			sig.SetValue(ctx, 20)
			gotCh <- sig.Get(ctx)
			return nil
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(setAct.OnClick())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)

	_, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	time.Sleep(20 * time.Millisecond)

	triggerAction(t, server.URL, ctxID, actionID)

	select {
	case got := <-gotCh:
		assert.Equal(t, 20, got, "second SetValue must overwrite the first")
	case <-time.After(sseTimeout):
		t.Fatal("timed out waiting for action")
	}
}

func TestSignal_nilInitialCreatesError(t *testing.T) {
	v := via.New()
	var errVal error
	v.Page("/", func(cmp *via.Cmp) {
		sig := via.Signal[any](cmp, nil)
		errVal = sig.Err()
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div() })
	})
	require.Error(t, errVal)
}
