package picocss_test

import (
	"compress/gzip"
	"html"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/picocss"
	"github.com/stretchr/testify/assert"
)

// fakeCDNTransport intercepts CDN requests and returns fake CSS,
// eliminating network I/O from tests.
type fakeCDNTransport struct{ real http.RoundTripper }

func (f *fakeCDNTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if strings.HasPrefix(req.URL.Host, "cdn.jsdelivr.net") {
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"text/css"}},
			Body:       io.NopCloser(strings.NewReader("/* fake pico css */")),
		}, nil
	}
	return f.real.RoundTrip(req)
}

func TestMain(m *testing.M) {
	http.DefaultTransport = &fakeCDNTransport{real: http.DefaultTransport}
	os.Exit(m.Run())
}

// --- Theme type ---

func TestPicoTheme_Constants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		constant picocss.PicoTheme
		want     string
	}{
		{"Amber", picocss.PicoThemeAmber, "amber"},
		{"Blue", picocss.PicoThemeBlue, "blue"},
		{"Cyan", picocss.PicoThemeCyan, "cyan"},
		{"Fuchsia", picocss.PicoThemeFuchsia, "fuchsia"},
		{"Green", picocss.PicoThemeGreen, "green"},
		{"Grey", picocss.PicoThemeGrey, "grey"},
		{"Indigo", picocss.PicoThemeIndigo, "indigo"},
		{"Jade", picocss.PicoThemeJade, "jade"},
		{"Lime", picocss.PicoThemeLime, "lime"},
		{"Orange", picocss.PicoThemeOrange, "orange"},
		{"Pink", picocss.PicoThemePink, "pink"},
		{"Pumpkin", picocss.PicoThemePumpkin, "pumpkin"},
		{"Purple", picocss.PicoThemePurple, "purple"},
		{"Red", picocss.PicoThemeRed, "red"},
		{"Sand", picocss.PicoThemeSand, "sand"},
		{"Slate", picocss.PicoThemeSlate, "slate"},
		{"Violet", picocss.PicoThemeViolet, "violet"},
		{"Yellow", picocss.PicoThemeYellow, "yellow"},
		{"Zinc", picocss.PicoThemeZinc, "zinc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, string(tt.constant))
		})
	}
}

func TestPicoTheme_Type(t *testing.T) {
	t.Parallel()

	var theme picocss.PicoTheme = picocss.PicoThemeBlue
	assert.Equal(t, "blue", string(theme))
}

func TestPicoTheme_StringConversion(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "amber", string(picocss.PicoThemeAmber))
	assert.Equal(t, "blue", string(picocss.PicoThemeBlue))
	assert.Equal(t, "zinc", string(picocss.PicoThemeZinc))
}

func TestPicoTheme_StringMethod(t *testing.T) {
	t.Parallel()

	assert.Equal(t, "amber", picocss.PicoThemeAmber.String())
	assert.Equal(t, "blue", picocss.PicoThemeBlue.String())
	assert.Equal(t, "purple", picocss.PicoThemePurple.String())
	assert.Equal(t, "red", picocss.PicoThemeRed.String())
	assert.Equal(t, "zinc", picocss.PicoThemeZinc.String())
}

func TestAllPicoThemes_ContainsAllThemes(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 19, len(picocss.AllPicoThemes))
	expected := []picocss.PicoTheme{
		picocss.PicoThemeAmber, picocss.PicoThemeBlue, picocss.PicoThemeCyan,
		picocss.PicoThemeFuchsia, picocss.PicoThemeGreen, picocss.PicoThemeGrey,
		picocss.PicoThemeIndigo, picocss.PicoThemeJade, picocss.PicoThemeLime,
		picocss.PicoThemeOrange, picocss.PicoThemePink, picocss.PicoThemePumpkin,
		picocss.PicoThemePurple, picocss.PicoThemeRed, picocss.PicoThemeSand,
		picocss.PicoThemeSlate, picocss.PicoThemeViolet, picocss.PicoThemeYellow,
		picocss.PicoThemeZinc,
	}
	assert.Equal(t, expected, picocss.AllPicoThemes)
}

func TestAllPicoThemes_Count(t *testing.T) {
	t.Parallel()

	assert.Equal(t, 19, len(picocss.AllPicoThemes))
}

func TestAllPicoThemes_NoDuplicates(t *testing.T) {
	t.Parallel()

	seen := make(map[string]bool)
	for _, theme := range picocss.AllPicoThemes {
		assert.False(t, seen[string(theme)], "duplicate theme: %s", theme)
		seen[string(theme)] = true
	}
}

// --- Constructor ---

func TestPlugin_ReturnsViaPlugin(t *testing.T) {
	t.Parallel()

	p := picocss.Plugin()
	assert.NotNil(t, p)
	var _ via.Plugin = p
}

func TestPlugin_Defaults(t *testing.T) {
	t.Parallel()

	p := picocss.Plugin()
	assert.NotNil(t, p)
}

func TestPlugin_WithThemes(t *testing.T) {
	t.Parallel()

	p := picocss.Plugin(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemePurple, picocss.PicoThemeAmber}))
	assert.NotNil(t, p)
}

func TestPlugin_WithDefaultTheme(t *testing.T) {
	t.Parallel()

	p := picocss.Plugin(picocss.WithDefaultTheme(picocss.PicoThemePurple))
	assert.NotNil(t, p)
}

func TestPlugin_WithClassless(t *testing.T) {
	t.Parallel()

	p := picocss.Plugin(picocss.WithClassless())
	assert.NotNil(t, p)
}

func TestPlugin_WithColorClasses(t *testing.T) {
	t.Parallel()

	p := picocss.Plugin(picocss.WithColorClasses())
	assert.NotNil(t, p)
}

func TestPlugin_WithDarkMode(t *testing.T) {
	t.Parallel()

	p := picocss.Plugin(picocss.WithDarkMode())
	assert.NotNil(t, p)
}

func TestPlugin_WithLightMode(t *testing.T) {
	t.Parallel()

	p := picocss.Plugin(picocss.WithLightMode())
	assert.NotNil(t, p)
}

func TestPlugin_WithMultipleOptions(t *testing.T) {
	t.Parallel()

	p := picocss.Plugin(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed, picocss.PicoThemeGreen}),
		picocss.WithDefaultTheme(picocss.PicoThemeRed),
		picocss.WithClassless(),
		picocss.WithColorClasses(),
	)
	assert.NotNil(t, p)
}

func TestPlugin_AllOptions(t *testing.T) {
	t.Parallel()

	p := picocss.Plugin(
		picocss.WithThemes(picocss.AllPicoThemes),
		picocss.WithDefaultTheme(picocss.PicoThemeBlue),
		picocss.WithClassless(),
		picocss.WithColorClasses(),
		picocss.WithDarkMode(),
	)
	assert.NotNil(t, p)
}

func TestPlugin_OnlyColorClasses(t *testing.T) {
	t.Parallel()

	assert.NotNil(t, picocss.Plugin(picocss.WithColorClasses()))
}

func TestPlugin_OnlyClassless(t *testing.T) {
	t.Parallel()

	assert.NotNil(t, picocss.Plugin(picocss.WithClassless()))
}

func TestPlugin_SingleTheme(t *testing.T) {
	t.Parallel()

	assert.NotNil(t, picocss.Plugin(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed})))
}

func TestPlugin_DifferentDefaultThemes(t *testing.T) {
	t.Parallel()

	for _, theme := range []picocss.PicoTheme{
		picocss.PicoThemeAmber, picocss.PicoThemeBlue, picocss.PicoThemePurple,
	} {
		t.Run(string(theme), func(t *testing.T) {
			assert.NotNil(t, picocss.Plugin(picocss.WithDefaultTheme(theme)))
		})
	}
}

func TestPlugin_RepeatedOptions(t *testing.T) {
	t.Parallel()

	// Last call wins for same option type
	p := picocss.Plugin(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed}),
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemeGreen}),
	)
	assert.NotNil(t, p)
}

// --- Integration (CDN fetch required) ---

func registerPlugin(opts ...picocss.PicoOption) (*via.App, *httptest.Server) {
	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	v.Page("/", func(cmp *via.Cmp) {
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("x")) })
	})
	picocss.Plugin(opts...).Register(v)
	return v, server
}

func TestPlugin_NoOptionsDefaultsToSingleAmber(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin()
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp2, err := http.Get(server.URL + "/_plugins/picocss/theme/blue")
	assert.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestPlugin_EmptyThemesList_DefaultsToSingleAmber(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin(picocss.WithThemes([]picocss.PicoTheme{}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestPlugin_WithInvalidDefaultTheme(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed, picocss.PicoThemeGreen}),
		picocss.WithDefaultTheme(picocss.PicoThemeBlue), // not in list
	)
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/red")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	resp2, err := http.Get(server.URL + "/_plugins/picocss/theme/blue")
	assert.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestPlugin_RepeatedThemes_Deduplicated(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin(picocss.WithThemes([]picocss.PicoTheme{
		picocss.PicoThemeBlue, picocss.PicoThemeBlue, picocss.PicoThemeRed, picocss.PicoThemeBlue,
	}))
	defer server.Close()

	for _, theme := range []string{"blue", "red"} {
		resp, err := http.Get(server.URL + "/_plugins/picocss/theme/" + theme)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPlugin_DuplicateThemesWithDefaultInDuplicates(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemeBlue, picocss.PicoThemeRed}),
		picocss.WithDefaultTheme(picocss.PicoThemeBlue),
	)
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/blue")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestPlugin_ServesDefaultTheme(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin()
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/css", resp.Header.Get("Content-Type"))
}

func TestPlugin_InvalidThemeReturns404(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin()
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/nonexistent")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPlugin_WithThemes_ServesOnlyConfiguredThemes(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemeRed}))
	defer server.Close()

	for _, theme := range []string{"blue", "red"} {
		resp, err := http.Get(server.URL + "/_plugins/picocss/theme/" + theme)
		assert.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		resp.Body.Close()
	}

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// --- Signal and binding ---

func pageBody(server *httptest.Server) string {
	resp, err := http.Get(server.URL + "/")
	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return html.UnescapeString(string(body))
}

func TestPlugin_InitializesSignals(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin()
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `data-signals`)
	assert.Contains(t, body, `_picoTheme`)
	assert.Contains(t, body, `_picoDarkMode`)
}

func TestPlugin_DefaultThemeInSignal(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemePurple, picocss.PicoThemeAmber}),
		picocss.WithDefaultTheme(picocss.PicoThemePurple),
	)
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `_picoTheme`)
	assert.Contains(t, body, `purple`)
}

func TestPlugin_BindsDataThemeToDarkModeSignal(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin()
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `data-attr:data-theme`)
	assert.Contains(t, body, `$_picoDarkMode`)
	assert.Contains(t, body, `dark`)
	assert.Contains(t, body, `light`)
}

func TestPlugin_DataThemeUsesLight(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin()
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, "light")
	assert.NotContains(t, body, "white")
}

// --- Dark mode options ---

func TestPlugin_DefaultDarkMode_UsesSystemPreference(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin()
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `"_picoDarkMode":"system"`)
	assert.Contains(t, body, `prefers-color-scheme`)
}

func TestPlugin_WithDarkMode_SignalIsDark(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin(picocss.WithDarkMode())
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `"_picoDarkMode":"dark"`)
}

func TestPlugin_WithLightMode_SignalIsLight(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin(picocss.WithLightMode())
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `"_picoDarkMode":"light"`)
}

// --- ETag ---

func TestPlugin_ThemeAsset_HasETagHeader(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin()
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.NotEmpty(t, resp.Header.Get("ETag"))
}

func TestPlugin_ThemeAsset_Returns304ForMatchingETag(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin()
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	etag := resp.Header.Get("ETag")
	resp.Body.Close()
	assert.NotEmpty(t, etag)

	req, _ := http.NewRequest("GET", server.URL+"/_plugins/picocss/theme/amber", nil)
	req.Header.Set("If-None-Match", etag)
	resp2, err := http.DefaultClient.Do(req)
	assert.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusNotModified, resp2.StatusCode)
}

// --- Gzip ---

func TestPlugin_ThemeAsset_ServesGzipWhenAccepted(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin()
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/_plugins/picocss/theme/amber", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))

	gr, err := gzip.NewReader(resp.Body)
	assert.NoError(t, err)
	defer gr.Close()
	css, err := io.ReadAll(gr)
	assert.NoError(t, err)
	assert.NotEmpty(t, css)
}

func TestPlugin_ThemeAsset_ServesPlainWhenGzipNotAccepted(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin()
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Empty(t, resp.Header.Get("Content-Encoding"))
	body, _ := io.ReadAll(resp.Body)
	assert.NotEmpty(t, body)
}

// --- Color classes ---

func TestPlugin_ColorClassesAsset_HasETagHeader(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin(picocss.WithColorClasses())
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/color-classes")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("ETag"))
}

func TestPlugin_ColorClassesAsset_ServesGzipWhenAccepted(t *testing.T) {
	t.Parallel()

	_, server := registerPlugin(picocss.WithColorClasses())
	defer server.Close()

	req, _ := http.NewRequest("GET", server.URL+"/_plugins/picocss/color-classes", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	client := &http.Client{Transport: &http.Transport{DisableCompression: true}}
	resp, err := client.Do(req)
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, "gzip", resp.Header.Get("Content-Encoding"))
}

// --- Get/Set API ---

func TestPlugin_SetDarkModeInInitAppearsInHTML(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	picocss.Plugin().Register(v)
	v.Page("/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			picocss.DarkModeSig().SetValue(ctx, "dark")
		})
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("x")) })
	})
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `"_picoDarkMode":"dark"`)
}

func TestPlugin_SetThemeInInitAppearsInHTML(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	picocss.Plugin(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemeAmber}),
	).Register(v)
	v.Page("/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			picocss.ThemeSig().SetValue(ctx, "blue")
		})
		cmp.View(func(ctx *via.Ctx) h.H { return h.Div(h.Text("x")) })
	})
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `"_picoTheme":"blue"`)
}

func TestPlugin_GetDarkModeReturnsSetValue(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	picocss.Plugin().Register(v)
	v.Page("/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			picocss.DarkModeSig().SetValue(ctx, "light")
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("dm=%s", picocss.DarkModeSig().Get(ctx)))
		})
	})
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, "dm=light")
}

func TestPlugin_GetThemeReturnsSetValue(t *testing.T) {
	t.Parallel()

	var server *httptest.Server
	v := via.New(via.WithTestServer(&server))
	picocss.Plugin(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemePurple, picocss.PicoThemeAmber}),
	).Register(v)
	v.Page("/", func(cmp *via.Cmp) {
		cmp.Init(func(ctx *via.Ctx) {
			picocss.ThemeSig().SetValue(ctx, "purple")
		})
		cmp.View(func(ctx *via.Ctx) h.H {
			return h.Div(h.Textf("theme=%s", picocss.ThemeSig().Get(ctx)))
		})
	})
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, "theme=purple")
}
