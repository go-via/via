package via_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type liveTabsPage struct{}

func (p *liveTabsPage) View(ctx *via.CtxR) h.H { return h.Div() }

func TestLiveTabs_reflectsRegisteredCount(t *testing.T) {
	t.Parallel()

	app := via.New()
	server := vt.Serve(t, app)
	via.Mount[liveTabsPage](app, "/")

	assert.Equal(t, 0, app.LiveTabs(), "starts at zero")

	for i := 1; i <= 3; i++ {
		resp, err := server.Client().Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, i, app.LiveTabs(),
			"each fresh page render registers one ctx")
	}
}
