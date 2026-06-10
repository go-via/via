package core_test

import (
	"testing"

	"github.com/go-via/viashowcase/internal/core"
	"github.com/stretchr/testify/assert"
)

func TestIsAllowedAvatarType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		ct   string
		want bool
	}{
		{"png is a safe raster image", "image/png", true},
		{"jpeg is allowed", "image/jpeg", true},
		{"gif is allowed", "image/gif", true},
		{"webp is allowed", "image/webp", true},
		{"sniffed jpeg with a charset param is allowed", "image/jpeg; charset=binary", true},
		{"media type matched case-insensitively", "IMAGE/PNG", true},
		{"surrounding spaces are tolerated", "  image/png ", true},

		// The whole point: these must never be served inline.
		{"html is rejected (stored-XSS vector)", "text/html; charset=utf-8", false},
		{"plain text is rejected", "text/plain; charset=utf-8", false},
		{"svg is rejected because it can carry script", "image/svg+xml", false},
		{"octet-stream is rejected", "application/octet-stream", false},
		{"empty is rejected", "", false},
		{"a non-image image-prefixed type is not allowed", "image/tiff", false},
		{"a type that merely starts with an allowed name is rejected", "image/pngx", false},
		{"a vendor-prefixed lookalike is rejected", "x-image/png", false},
		{"a bare image type with no subtype is rejected", "image/", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, core.IsAllowedAvatarType(tt.ct))
		})
	}
}
