package site_test

import (
	"net/http"
	"strings"
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
		{"protocol-relative base", "v0.8=//evil.example"},
		{"backslash base", `v0.8=/\evil.example`},
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

func TestSite_pickerOffersLatestFirst(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		opts     site.Options
		path     string
		summary  string
		latest   string
		external bool
	}{
		{"current is latest", site.Options{Versions: []shell.Version{{Label: "v0.9"}, {Label: "v0.8", Base: "/v0.8"}}},
			"/signals", `>v0.9 (latest)</summary>`, `<li><a href="/signals" aria-current="page">latest (v0.9)</a></li><li><a href="/v0.8/signals">v0.8</a></li></ul>`, false},
		{"archived build", site.Options{Base: "/v0.8", Versions: []shell.Version{{Label: "v0.9"}, {Label: "v0.8", Base: "/v0.8"}}},
			"/v0.8/signals", `>v0.8</summary>`, `<li><a href="/signals">latest (v0.9)</a></li>`, false},
		{"latest hosted elsewhere", site.Options{Base: "/v0.8", Versions: []shell.Version{{Label: "v0.9", Base: "https://example.org/via/"}, {Label: "v0.8", Base: "/v0.8"}}},
			"/v0.8/signals", `>v0.8</summary>`, `<li><a href="https://example.org/via/" target="_blank" rel="noopener">latest (v0.9)<svg`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, body := get(t, siteServer(t, tt.opts), tt.path, nil)
			require.Equal(t, http.StatusOK, resp.StatusCode)
			picker := body[strings.Index(body, `<details class="versions">`):]
			picker = picker[:strings.Index(picker, `</details>`)]
			assert.Contains(t, picker, tt.summary)
			assert.Contains(t, picker, `<ul>`+tt.latest, "latest is the first entry")
		})
	}
}

func TestSite_redirectsLatestToTheNewestVersion(t *testing.T) {
	t.Parallel()
	root := []shell.Version{{Label: "v0.9"}, {Label: "v0.8", Base: "/v0.8"}}
	ext := []shell.Version{{Label: "v1.0", Base: "https://example.org/via/"}, {Label: "v0.9"}}
	tests := []struct {
		name     string
		versions []shell.Version
		path     string
		want     string
	}{
		{"bare", root, "/latest", "/"},
		{"trailing slash", root, "/latest/", "/"},
		{"page and query", root, "/latest/signals?x=1", "/signals?x=1"},
		{"page with trailing slash", root, "/latest/signals/", "/signals"},
		{"unknown page lands on the latest's 404", root, "/latest/nope", "/nope"},
		{"external latest", ext, "/latest/signals?x=1", "https://example.org/via/"},
		{"external latest bare", ext, "/latest", "https://example.org/via/"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			resp, _ := get(t, siteServer(t, site.Options{Versions: tt.versions}), tt.path, nil)
			assert.Equal(t, http.StatusFound, resp.StatusCode, "302: the target moves on each release")
			assert.Equal(t, tt.want, resp.Header.Get("Location"))
		})
	}
}

func TestSite_latestNeverRedirectsOffHost(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{Versions: []shell.Version{{Label: "v0.9"}, {Label: "v0.8", Base: "/v0.8"}}})
	// Browsers read a leading /\ like //, so "/\evil.example" would leave the host.
	for _, p := range []string{`/latest/%5Cevil.example`, `/latest//evil.example`, `/latest/%2F%2Fevil.example`} {
		resp, _ := get(t, srv, p, nil)
		loc := resp.Header.Get("Location")
		assert.False(t, strings.HasPrefix(loc, "//") || strings.HasPrefix(loc, `/\`), "%s redirected to %q", p, loc)
	}
}

func TestSite_answersLatestOnlyAtTheRoot(t *testing.T) {
	t.Parallel()
	srv := siteServer(t, site.Options{Base: "/v0.8", Versions: []shell.Version{{Label: "v0.9"}, {Label: "v0.8", Base: "/v0.8"}}})
	resp, _ := get(t, srv, "/latest/signals", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp, _ = get(t, srv, "/v0.8/latest", nil)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)

	_, body := get(t, siteServer(t, site.Options{Origin: "https://go-via.dev", Versions: []shell.Version{{Label: "v0.9"}}}), "/sitemap.xml", nil)
	assert.NotContains(t, body, "/latest")
}
