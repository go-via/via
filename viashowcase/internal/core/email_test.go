package core_test

import (
	"testing"

	"github.com/go-via/viashowcase/internal/core"
	"github.com/stretchr/testify/assert"
)

// Email must be case- and whitespace-insensitive: a user who signs up as
// "Bob@Test.dev" must be able to log in as "bob@test.dev", and case variants
// must not create duplicate accounts.
func TestNormalizeEmail(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"mixed case lowercased", "Bob@Test.dev", "bob@test.dev"},
		{"surrounding whitespace trimmed", "  bob@test.dev  ", "bob@test.dev"},
		{"mixed case and whitespace together", "  Bob@Test.DEV \t", "bob@test.dev"},
		{"already normalized is unchanged", "bob@test.dev", "bob@test.dev"},
		{"empty stays empty", "", ""},
		{"accented uppercase domain lowercases", "user@EXÄMPLE.com", "user@exämple.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, core.NormalizeEmail(tt.in))
		})
	}
}
