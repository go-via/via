package site_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/shell"
	"go-via.dev/site/site"
)

func TestParseVersions_readsLabelBasePairs(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want []shell.Version
	}{
		{"empty is no picker", "", nil},
		{"root is the empty base", "v0.8=/", []shell.Version{{Label: "v0.8", Base: ""}}},
		{"trailing slash dropped", "v0.8=/, v0.7=/v0.7/", []shell.Version{{Label: "v0.8"}, {Label: "v0.7", Base: "/v0.7"}}},
		{"external URL kept as is", "v0.8=/,v0.7=https://go-via.github.io/via/",
			[]shell.Version{{Label: "v0.8"}, {Label: "v0.7", Base: "https://go-via.github.io/via/"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := site.ParseVersions(tt.raw)
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestParseVersions_refusesMalformedInput(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
	}{
		{"missing =", "v0.8"},
		{"empty label", "=/"},
		{"relative base", "v0.8=v0.8"},
		{"only the first = splits", "a=b=/x"},
		{"duplicate label", "v0.8=/,v0.8=/v0.8"},
		{"duplicate base", "v0.9=/,v0.8=/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, err := site.ParseVersions(tt.raw)
			require.Error(t, err)
			assert.Contains(t, err.Error(), "site: VIA_VERSIONS")
		})
	}
}
