package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type maxCtxPage struct{}

func (p *maxCtxPage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestMaxContexts_rejectsBeyondCap(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(
		via.WithMaxContexts(2),
		via.WithTestServer(&server),
	)
	via.Mount[maxCtxPage](app, "/")
	defer server.Close()

	for i := 0; i < 2; i++ {
		resp, err := http.Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"first %d requests should fit under the cap", 2)
	}

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusServiceUnavailable, resp.StatusCode,
		"third request should be 503 with cap=2")
}

func TestMaxContexts_zeroDisablesTheCap(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server)) // no WithMaxContexts
	via.Mount[maxCtxPage](app, "/")
	defer server.Close()

	for i := 0; i < 5; i++ {
		resp, err := http.Get(server.URL + "/")
		require.NoError(t, err)
		resp.Body.Close()
		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"unset cap should not reject any request")
	}
}
