package site

import (
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
)

func TestUnavailableDetail_blamesTheStoreOnlyWhenItWasDown(t *testing.T) {
	t.Parallel()
	assert.Contains(t, unavailableDetail(via.PageError{Reason: via.ReasonUnavailable, Err: via.ErrStoreDown}), "session store")
	assert.NotContains(t, unavailableDetail(via.PageError{Reason: via.ReasonUnavailable}), "session store")
}
