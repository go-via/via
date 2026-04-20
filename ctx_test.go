package via_test

import (
	"testing"
	"time"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
)

func TestCtx_sessionTTL_defaultsTo30Minutes(t *testing.T) {
	t.Parallel()
	app := via.New()
	assert.Equal(t, 30*time.Minute, app.Config().SessionTTL())
}

func TestCtx_defaultTitle_isVia(t *testing.T) {
	t.Parallel()
	app := via.New()
	assert.Equal(t, "Via", app.Config().Title())
}

func TestCtx_defaultAddr_is3000(t *testing.T) {
	t.Parallel()
	app := via.New()
	assert.Equal(t, ":3000", app.Config().Addr())
}
