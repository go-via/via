package core_test

import (
	"testing"

	"github.com/go-via/viashowcase/internal/core"
	"github.com/stretchr/testify/assert"
)

// The signup form promises "at least 8 characters"; the policy must enforce
// exactly that, measured in characters (runes) so the count matches what a
// user typed, not the byte length.
func TestPasswordLongEnough(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		pw   string
		want bool
	}{
		{"empty is too short", "", false},
		{"seven chars is too short", "1234567", false},
		{"exactly eight chars is allowed", "12345678", true},
		{"long password is allowed", "supersecret1", true},
		{"eight multibyte runes count as eight characters", "日本語日本語日本", true},
		{"seven multibyte runes are too short", "日本語日本語日", false},
		{"bytes don't count: five emoji (20 bytes) is too short", "😀😁😂🤣😎", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, core.PasswordLongEnough(tt.pw))
		})
	}
}
