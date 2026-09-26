package actions_test

import (
	"bytes"
	"io"
	"mime/multipart"
	"net/http"
	"regexp"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/vt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/snippet/src/actions"
)

var formAction = regexp.MustCompile(`<form method="post" enctype="multipart/form-data" action="([^"]+)"`)

func submit(t *testing.T, app *vt.App, fields map[string]string) string {
	t.Helper()
	_, page := app.Get("/")
	m := formAction.FindStringSubmatch(page)
	require.NotNil(t, m, "no PostForm on the page")
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	for k, v := range fields {
		require.NoError(t, mw.WriteField(k, v))
	}
	require.NoError(t, mw.Close())
	req, err := http.NewRequest(http.MethodPost, app.URL()+m[1], &body)
	require.NoError(t, err)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := app.Client().Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)
	b, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	return string(b)
}

func TestProfile_submitPutsTheFormValuesBackIntoTheSignals(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(actions.Profile{}, via.WithLogger(vt.Logger(t))))
	page := submit(t, app, map[string]string{"name": " ada ", "country": "br", "region": "Bahia", "_viatab": ""})
	assert.Contains(t, page, `"name":"ada"`)
	assert.Contains(t, page, `"country":"br"`)
	assert.Contains(t, page, `"region":"Bahia"`)
	assert.Contains(t, page, `<option value="Bahia">`, "the region list follows the submitted country")
}

func TestProfile_submitWithoutANameKeepsTheOtherFields(t *testing.T) {
	t.Parallel()
	app := vt.Serve(t, via.Handler(actions.Profile{}, via.WithLogger(vt.Logger(t))))
	page := submit(t, app, map[string]string{"name": "", "country": "br", "region": "Pará", "_viatab": ""})
	assert.Contains(t, page, "Name is required.")
	assert.Contains(t, page, `"region":"Pará"`)
}
