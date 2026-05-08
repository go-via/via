package via_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHSTS_defaultHeaderHasOneYearAndSubdomains(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.HSTS())
	app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()

	got := resp.Header.Get("Strict-Transport-Security")
	assert.Equal(t, "max-age=31536000; includeSubDomains", got)
}

func TestHSTS_optionsCustomiseHeader(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	app := via.New(via.WithTestServer(&server))
	app.Use(via.HSTS(
		via.HSTSMaxAge(60*60*24*30), // 30 days
		via.HSTSIncludeSubdomains(false),
		via.HSTSPreload(true),
	))
	app.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {})
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	require.NoError(t, err)
	defer resp.Body.Close()
	got := resp.Header.Get("Strict-Transport-Security")
	assert.Equal(t, "max-age=2592000; preload", got,
		"options should produce: 30d, no subdomains, with preload")
}
