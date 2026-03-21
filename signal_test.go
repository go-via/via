package via_test

import (
	"fmt"
	"net/http"
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
	v.Page("/", func(c *via.Context) {
		sig := via.Signal(c, "hello")
		got = sig.Get(c)
		c.View(func() h.H { return h.Div() })
	})
	assert.Equal(t, "hello", got)
}

func TestSignal_getReturnsTypedValue(t *testing.T) {
	v := via.New()
	var got int
	v.Page("/", func(c *via.Context) {
		sig := via.Signal(c, 42)
		got = sig.Get(c)
		c.View(func() h.H { return h.Div() })
	})
	assert.Equal(t, 42, got)
}

func TestSignal_idReturnsNonEmpty(t *testing.T) {
	v := via.New()
	var idA, idB string
	v.Page("/", func(c *via.Context) {
		a := via.Signal(c, "a")
		b := via.Signal(c, "b")
		idA = a.ID()
		idB = b.ID()
		c.View(func() h.H { return h.Div() })
	})
	require.NotEmpty(t, idA)
	require.NotEmpty(t, idB)
	assert.NotEqual(t, idA, idB)
}

func TestSignal_sliceSerializesForTransport(t *testing.T) {
	v := via.New()
	var got []string
	v.Page("/", func(c *via.Context) {
		sig := via.Signal(c, []string{"a", "b"})
		got = sig.Get(c)
		c.View(func() h.H { return h.Div() })
	})
	assert.Equal(t, []string{"a", "b"}, got)
}

func TestSignal_bindRendersDataBindAttr(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return via.Signal(c, "x") })
	out := renderH(t, h.Input(sig.Bind()))
	assert.Contains(t, out, "data-bind")
	assert.Contains(t, out, sig.ID())
}

func TestSignal_textRendersDataTextSpan(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return via.Signal(c, "world") })
	out := renderH(t, h.Div(sig.Text()))
	assert.Contains(t, out, "<span")
	assert.Contains(t, out, "data-text")
	assert.Contains(t, out, sig.ID())
}

func TestSignal_showRendersDataShowAttr(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return via.Signal(c, true) })
	out := renderH(t, h.Div(sig.Show()))
	assert.Contains(t, out, "data-show")
	assert.Contains(t, out, sig.ID())
}

func TestSignal_tagPrependsLabel(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT {
		s := via.Signal(c, "")
		s.Tag("search")
		return s
	})
	assert.Contains(t, sig.Ref(), "search")
}

func TestSignal_refReturnsDollarID(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return via.Signal(c, "x") })
	assert.Equal(t, "$"+sig.ID(), sig.Ref())
}

func TestSignal_tagAffectsBindID(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT {
		s := via.Signal(c, "")
		s.Tag("myfield")
		return s
	})
	out := renderH(t, h.Input(sig.Bind()))
	assert.Contains(t, out, "myfield")
}

func TestSignal_setValueUpdatesGet(t *testing.T) {
	v := via.New()
	var got string
	v.Page("/", func(c *via.Context) {
		sig := via.Signal(c, "old")
		sig.SetValue("new")
		got = sig.Get(c)
		c.View(func() h.H { return h.Div() })
	})
	assert.Equal(t, "new", got)
}

// TestSignal_intGetAfterNumericJSONInjection verifies that an int signal receives the correct value
// when the browser sends it as a JSON number (float64 after json.Unmarshal into map[string]any).
func TestSignal_intGetAfterNumericJSONInjection(t *testing.T) {
	gotCh := make(chan int, 1)
	server := newTestApp(t, "/", func(c *via.Context) {
		sig := via.Signal(c, 1000)
		act := c.Action(func() error {
			gotCh <- sig.Get(c)
			return nil
		})
		c.View(func() h.H {
			return h.Div(sig.Bind(), act.OnChange())
		})
	})

	body := getPageBody(t, server, "/")
	ctxID := extractCtxID(t, body)
	actionID := extractActionID(t, body)
	sigID := extractSignalID(t, body)

	stream, cancel := connectSSE(t, server, ctxID)
	defer cancel()
	readSSEEvent(t, stream, sseTimeout)
	time.Sleep(20 * time.Millisecond)

	// Send signal value as a JSON number (no quotes) — same as what the browser sends.
	sigsJSON := fmt.Sprintf(`{"via-ctx":%q,%q:500}`, ctxID, sigID)
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

// TestSignal_intInitialValueSerializesAsJSONNumber verifies that an int signal is embedded in the
// initial page HTML as a JSON number (not a quoted string). This ensures the browser stores it as a
// number so the round-trip through data-bind → action POST → injectSignals works correctly.
func TestSignal_intInitialValueSerializesAsJSONNumber(t *testing.T) {
	server := newTestApp(t, "/", func(c *via.Context) {
		sig := via.Signal(c, 1000)
		c.View(func() h.H { return h.Input(sig.Bind()) })
	})
	body := getPageBody(t, server, "/")
	sigID := extractSignalID(t, body)
	// The data-signals JSON is HTML-escaped: quotes become &#34;
	// A numeric value should appear without quotes: &#34;sigID&#34;:1000
	// A string value would appear as: &#34;sigID&#34;:&#34;1000&#34;
	numericEntry := "&#34;" + sigID + "&#34;:1000"
	assert.Contains(t, body, numericEntry, "int signal should be encoded as a JSON number, not a string")
}

func TestSignal_nilInitialCreatesError(t *testing.T) {
	v := via.New()
	var errVal error
	v.Page("/", func(c *via.Context) {
		sig := via.Signal[any](c, nil)
		errVal = sig.Err()
		c.View(func() h.H { return h.Div() })
	})
	require.Error(t, errVal)
}
