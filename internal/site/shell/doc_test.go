package shell_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go-via.dev/site/shell"
)

func TestSlug_makesAFragmentID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		title string
		want  string
	}{
		{"lower-cases", "Shutdown order", "shutdown-order"},
		{"collapses punctuation runs", "Sessions & security", "sessions-security"},
		{"trims the ends", "  (Composition)  ", "composition"},
		{"keeps digits", "Migrating from v0.7", "migrating-from-v0-7"},
		{"drops non-ASCII", "Café › menu", "caf-menu"},
		{"falls back when nothing is left", "…", "section"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, shell.Slug(tt.title))
		})
	}
}
