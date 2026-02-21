package picocss_test

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/picocss"
	"github.com/stretchr/testify/assert"
)

// --- Theme type ---

func TestPicoTheme_Constants(t *testing.T) {
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
	var theme picocss.PicoTheme = picocss.PicoThemeBlue
	assert.Equal(t, "blue", string(theme))
}

func TestPicoTheme_StringConversion(t *testing.T) {
	assert.Equal(t, "amber", string(picocss.PicoThemeAmber))
	assert.Equal(t, "blue", string(picocss.PicoThemeBlue))
	assert.Equal(t, "zinc", string(picocss.PicoThemeZinc))
}

func TestPicoTheme_StringMethod(t *testing.T) {
	assert.Equal(t, "amber", picocss.PicoThemeAmber.String())
	assert.Equal(t, "blue", picocss.PicoThemeBlue.String())
	assert.Equal(t, "purple", picocss.PicoThemePurple.String())
	assert.Equal(t, "red", picocss.PicoThemeRed.String())
	assert.Equal(t, "zinc", picocss.PicoThemeZinc.String())
}

func TestAllPicoThemes_ContainsAllThemes(t *testing.T) {
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
	assert.Equal(t, 19, len(picocss.AllPicoThemes))
}

func TestAllPicoThemes_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	for _, theme := range picocss.AllPicoThemes {
		assert.False(t, seen[string(theme)], "duplicate theme: %s", theme)
		seen[string(theme)] = true
	}
}

// --- Constructor ---

func TestNew_ReturnsViaPlugin(t *testing.T) {
	p := picocss.New()
	assert.NotNil(t, p)
	var _ via.Plugin = p
}

func TestNew_Defaults(t *testing.T) {
	p := picocss.New()
	assert.NotNil(t, p)
}

func TestNew_WithThemes(t *testing.T) {
	p := picocss.New(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemePurple, picocss.PicoThemeAmber}))
	assert.NotNil(t, p)
}

func TestNew_WithDefaultTheme(t *testing.T) {
	p := picocss.New(picocss.WithDefaultTheme(picocss.PicoThemePurple))
	assert.NotNil(t, p)
}

func TestNew_WithClassless(t *testing.T) {
	p := picocss.New(picocss.WithClassless())
	assert.NotNil(t, p)
}

func TestNew_WithColorClasses(t *testing.T) {
	p := picocss.New(picocss.WithColorClasses())
	assert.NotNil(t, p)
}

func TestNew_WithDarkMode(t *testing.T) {
	p := picocss.New(picocss.WithDarkMode())
	assert.NotNil(t, p)
}

func TestNew_WithLightMode(t *testing.T) {
	p := picocss.New(picocss.WithLightMode())
	assert.NotNil(t, p)
}

func TestNew_WithMultipleOptions(t *testing.T) {
	p := picocss.New(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed, picocss.PicoThemeGreen}),
		picocss.WithDefaultTheme(picocss.PicoThemeRed),
		picocss.WithClassless(),
		picocss.WithColorClasses(),
	)
	assert.NotNil(t, p)
}

func TestNew_AllOptions(t *testing.T) {
	p := picocss.New(
		picocss.WithThemes(picocss.AllPicoThemes),
		picocss.WithDefaultTheme(picocss.PicoThemeBlue),
		picocss.WithClassless(),
		picocss.WithColorClasses(),
		picocss.WithDarkMode(),
	)
	assert.NotNil(t, p)
}

func TestNew_OnlyColorClasses(t *testing.T) {
	assert.NotNil(t, picocss.New(picocss.WithColorClasses()))
}

func TestNew_OnlyClassless(t *testing.T) {
	assert.NotNil(t, picocss.New(picocss.WithClassless()))
}

func TestNew_SingleTheme(t *testing.T) {
	assert.NotNil(t, picocss.New(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed})))
}

func TestNew_DifferentDefaultThemes(t *testing.T) {
	for _, theme := range []picocss.PicoTheme{
		picocss.PicoThemeAmber, picocss.PicoThemeBlue, picocss.PicoThemePurple,
	} {
		t.Run(string(theme), func(t *testing.T) {
			assert.NotNil(t, picocss.New(picocss.WithDefaultTheme(theme)))
		})
	}
}

func TestNew_RepeatedOptions(t *testing.T) {
	// Last call wins for same option type
	p := picocss.New(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed}),
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemeGreen}),
	)
	assert.NotNil(t, p)
}

// --- Integration (CDN fetch required) ---

func registerPlugin(opts ...picocss.PicoOption) (*via.V, *httptest.Server) {
	v := via.New()
	v.Page("/", func(c *via.Context) {
		c.View(func() h.H { return h.Div(h.Text("x")) })
	})
	picocss.New(opts...).Register(v)
	server := httptest.NewServer(v.HTTPServeMux())
	return v, server
}

func TestNew_NoOptionsDefaultsToSingleAmber(t *testing.T) {
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

func TestNew_EmptyThemesList_DefaultsToSingleAmber(t *testing.T) {
	_, server := registerPlugin(picocss.WithThemes([]picocss.PicoTheme{}))
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestNew_WithInvalidDefaultTheme(t *testing.T) {
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

func TestNew_RepeatedThemes_Deduplicated(t *testing.T) {
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

func TestNew_DuplicateThemesWithDefaultInDuplicates(t *testing.T) {
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
	_, server := registerPlugin()
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/css", resp.Header.Get("Content-Type"))
}

func TestPlugin_InvalidThemeReturns404(t *testing.T) {
	_, server := registerPlugin()
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/nonexistent")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPlugin_WithThemes_ServesOnlyConfiguredThemes(t *testing.T) {
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
	return string(body)
}

func TestPlugin_InitializesSignals(t *testing.T) {
	_, server := registerPlugin()
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `data-signals`)
	assert.Contains(t, body, `_picoTheme`)
	assert.Contains(t, body, `_picoDarkMode`)
}

func TestPlugin_DefaultThemeInSignal(t *testing.T) {
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
	_, server := registerPlugin()
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `data-attr:data-theme`)
	assert.Contains(t, body, `$_picoDarkMode`)
	assert.Contains(t, body, `dark`)
	assert.Contains(t, body, `light`)
}

func TestPlugin_DataThemeUsesLight(t *testing.T) {
	_, server := registerPlugin()
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, "light")
	assert.NotContains(t, body, "white")
}

// --- Dark mode options ---

func TestPlugin_DefaultDarkMode_UsesSystemPreference(t *testing.T) {
	_, server := registerPlugin()
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `prefers-color-scheme`)
	assert.Contains(t, body, `_picoDarkMode`)
}

func TestPlugin_WithDarkMode_SignalIsTrue(t *testing.T) {
	_, server := registerPlugin(picocss.WithDarkMode())
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `_picoDarkMode`)
	assert.Contains(t, body, `true`)
	assert.NotContains(t, body, `prefers-color-scheme`)
}

func TestPlugin_WithLightMode_SignalIsFalse(t *testing.T) {
	_, server := registerPlugin(picocss.WithLightMode())
	defer server.Close()

	body := pageBody(server)
	assert.Contains(t, body, `_picoDarkMode`)
	assert.Contains(t, body, `false`)
	assert.NotContains(t, body, `prefers-color-scheme`)
}

// --- ETag ---

func TestPlugin_ThemeAsset_HasETagHeader(t *testing.T) {
	_, server := registerPlugin()
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.NotEmpty(t, resp.Header.Get("ETag"))
}

func TestPlugin_ThemeAsset_Returns304ForMatchingETag(t *testing.T) {
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
	_, server := registerPlugin(picocss.WithColorClasses())
	defer server.Close()

	resp, err := http.Get(server.URL + "/_plugins/picocss/color-classes")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.NotEmpty(t, resp.Header.Get("ETag"))
}

func TestPlugin_ColorClassesAsset_ServesGzipWhenAccepted(t *testing.T) {
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
