package via

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Multipart text fields arrive on the wire as strings. They must stay strings
// in the signals map so decodeScalarInto coerces per the TARGET signal type:
// a Signal[string] keeps "true"/"007" verbatim (JSON-coercing here would turn
// them into a bool / drop the leading zero), while a Signal[int] still parses
// "42" at decode time. Coercing here was both redundant and lossy.
func TestReadMultipartSignals_keepsTextValuesAsStringsForTypedDecode(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	require.NoError(t, mw.WriteField("name", "true"))
	require.NoError(t, mw.WriteField("tag", "007"))
	require.NoError(t, mw.WriteField("count", "42"))
	require.NoError(t, mw.Close())

	r := httptest.NewRequest("POST", "/", &buf)
	r.Header.Set("Content-Type", mw.FormDataContentType())

	dst := map[string]any{}
	_, err := readMultipartSignals(r, 1<<20, dst)
	require.NoError(t, err)

	assert.Equal(t, "true", dst["name"],
		"a bool-looking text field must stay a string so a Signal[string] keeps it")
	assert.Equal(t, "007", dst["tag"],
		"a leading-zero text field must not be lost to numeric coercion")
	assert.Equal(t, "42", dst["count"],
		"numeric coercion happens at decode-into-target, not in the multipart reader")
}
