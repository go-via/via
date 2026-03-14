package via_test

import (
	"testing"

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

// TestSignal_bindRendersDataBindAttr verifies Bind() produces a data-bind attribute referencing the signal ID.
func TestSignal_bindRendersDataBindAttr(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return via.Signal(c, "x") })
	out := renderH(t, h.Input(sig.Bind()))
	assert.Contains(t, out, "data-bind")
	assert.Contains(t, out, sig.ID())
}

// TestSignal_textRendersDataTextSpan verifies Text() produces a span with data-text referencing the signal ID.
func TestSignal_textRendersDataTextSpan(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return via.Signal(c, "world") })
	out := renderH(t, h.Div(sig.Text()))
	assert.Contains(t, out, "<span")
	assert.Contains(t, out, "data-text")
	assert.Contains(t, out, sig.ID())
}

// TestSignal_showRendersDataShowAttr verifies Show() produces a data-show attribute.
func TestSignal_showRendersDataShowAttr(t *testing.T) {
	sig := captureSignal(func(c *via.Context) signalT { return via.Signal(c, true) })
	out := renderH(t, h.Div(sig.Show()))
	assert.Contains(t, out, "data-show")
	assert.Contains(t, out, sig.ID())
}

// TestSignal_tagPrependsLabel verifies Tag() affects Ref() output.
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

// TestSignal_tagAffectsBindID verifies that after Tag(), Bind() uses the display ID not the raw ID.
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

// TestSignal_nilInitialCreatesError verifies that a nil initial value produces an error signal.
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
