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

type marshalUnfriendly struct {
	C chan int
}

type unmarshalablePage struct {
	Bad via.Signal[marshalUnfriendly]
}

func (p *unmarshalablePage) View(ctx *via.Ctx) h.H { return h.Div() }

func TestWritePageDocument_marshalFailureStillRenders(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	via.Mount[unmarshalablePage](app, "/")
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	body := readAll(t, resp.Body)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	// A typed Signal whose value can't be JSON-encoded must not poison
	// the whole page render; the document still ships, the bad signal
	// is just omitted from the initial data-signals payload.
	assert.Contains(t, body, "<div>")
}
