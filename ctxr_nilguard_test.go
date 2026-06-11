package via_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
)

// A hand-constructed typed-nil *CtxR handed to a Read must return the zero value,
// not panic — CtxR's accessors guard a nil receiver consistently.
func TestRead_nilCtxRReturnsZeroWithoutPanic(t *testing.T) {
	t.Parallel()

	var app via.StateApp[int]
	var sess via.StateSess[string]
	nilR := (*via.CtxR)(nil)

	assert.NotPanics(t, func() { _ = app.Read(nilR) },
		"StateApp.Read(nil *CtxR) must not panic")
	assert.NotPanics(t, func() { _ = sess.Read(nilR) },
		"StateSess.Read(nil *CtxR) must not panic")
	assert.Equal(t, 0, app.Read(nilR), "nil CtxR yields the zero value")
	assert.Equal(t, "", sess.Read(nilR), "nil CtxR yields the zero value")
}
