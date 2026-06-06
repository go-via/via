package auth

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashVerifyRoundtrip(t *testing.T) {
	t.Parallel()
	h, err := Hash("s3cret")
	require.NoError(t, err)
	require.NotEqual(t, "s3cret", h)
	assert.True(t, Verify(h, "s3cret"))
}

func TestVerifyWrongPassword(t *testing.T) {
	t.Parallel()
	h, err := Hash("s3cret")
	require.NoError(t, err)
	assert.False(t, Verify(h, "nope"))
}

func TestVerifyRejectsNonHash(t *testing.T) {
	t.Parallel()
	assert.False(t, Verify("not-a-bcrypt-hash", "s3cret"))
}
