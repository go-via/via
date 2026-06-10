package store

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Internal test: strArray.Value/Scan and parseArray are the unexported codec
// that maps poll choices to/from a Postgres text[] literal. They can't be
// reached through the public API without a live database, but the escaping is
// pure and a bug here silently corrupts ballots (choices with commas, quotes,
// or backslashes), so the codec is exercised directly.

// A choice list survives a Value -> Scan round trip intact, including the
// characters that matter for the array-literal format.
func TestStrArrayRoundTrip(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"simple":          {"Pizza", "Sushi", "Tacos"},
		"comma in choice": {"Pizza, large", "Sushi"},
		"double quote":    {`say "hi"`},
		"backslash":       {`a\b`},
		"both quote and backslash": {`a\"b`},
		"empty string element":     {""},
		"unicode":                  {"日本語", "🍕"},
		"braces":                   {"{not an array}"},
		"single empty slice":       {},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			v, err := strArray(in).Value()
			require.NoError(t, err)
			lit, ok := v.(string)
			require.True(t, ok, "Value must produce a string literal")

			var got strArray
			require.NoError(t, got.Scan(lit))
			assert.Equal(t, in, []string(got))
		})
	}
}

// A nil slice serializes to the empty-array literal (never SQL NULL), so a
// poll with no choices stores cleanly.
func TestStrArrayNilSerializesToEmptyArray(t *testing.T) {
	t.Parallel()
	v, err := strArray(nil).Value()
	require.NoError(t, err)
	assert.Equal(t, "{}", v)
}

// Postgres only quotes elements that need it, so Scan must accept BOTH
// unquoted and quoted elements in the same literal.
func TestStrArrayScanAcceptsPostgresQuotingVariants(t *testing.T) {
	t.Parallel()
	var unquoted strArray
	require.NoError(t, unquoted.Scan("{Pizza,Sushi}"))
	assert.Equal(t, []string{"Pizza", "Sushi"}, []string(unquoted))

	var mixed strArray
	require.NoError(t, mixed.Scan(`{"Pizza, large",Sushi}`))
	assert.Equal(t, []string{"Pizza, large", "Sushi"}, []string(mixed))

	// Escaped quote and backslash inside a quoted element.
	var escaped strArray
	require.NoError(t, escaped.Scan(`{"say \"hi\"","a\\b"}`))
	assert.Equal(t, []string{`say "hi"`, `a\b`}, []string(escaped))
}

// Scan reads the wire types a SQL driver actually hands back ([]byte and
// string) and treats a NULL column as no choices.
func TestStrArrayScanWireTypes(t *testing.T) {
	t.Parallel()
	var fromBytes strArray
	require.NoError(t, fromBytes.Scan([]byte("{Pizza}")))
	assert.Equal(t, []string{"Pizza"}, []string(fromBytes))

	var fromNil strArray
	require.NoError(t, fromNil.Scan(nil))
	assert.Nil(t, []string(fromNil))

	var empty strArray
	require.NoError(t, empty.Scan("{}"))
	assert.Equal(t, []string{}, []string(empty))
}
