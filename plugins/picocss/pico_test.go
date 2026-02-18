package picocss_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/plugins/picocss"
	"github.com/stretchr/testify/assert"
)

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

func TestNew_ReturnsViaPlugin(t *testing.T) {
	p := picocss.New()

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestNew_Defaults(t *testing.T) {
	p := picocss.New()

	// Default theme should be Amber
	var _ via.Plugin = p
	// Can't check internal state from outside package,
	// but we can verify via behavior in integration tests
}

func TestNew_WithThemes(t *testing.T) {
	customThemes := []picocss.PicoTheme{
		picocss.PicoThemePurple,
		picocss.PicoThemeAmber,
	}
	p := picocss.New(picocss.WithThemes(customThemes))

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestNew_WithDefaultTheme(t *testing.T) {
	p := picocss.New(picocss.WithDefaultTheme(picocss.PicoThemePurple))

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestNew_WithClassless(t *testing.T) {
	p := picocss.New(picocss.WithClassless())

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestNew_WithColorClasses(t *testing.T) {
	p := picocss.New(picocss.WithColorClasses())

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestNew_WithMultipleOptions(t *testing.T) {
	p := picocss.New(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed, picocss.PicoThemeGreen}),
		picocss.WithDefaultTheme(picocss.PicoThemeRed),
		picocss.WithClassless(),
		picocss.WithColorClasses(),
	)

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestNew_WithInvalidDefaultTheme(t *testing.T) {
	// When default theme is not in themes list, should fall back to first theme (Red)
	p := picocss.New(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed, picocss.PicoThemeGreen}),
		picocss.WithDefaultTheme(picocss.PicoThemeBlue), // Blue not in list
	)
	v := via.New()
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	// Should serve Red (first in list, fallback from invalid Blue)
	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/red")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Should NOT serve Blue (was invalid default, not in themes)
	resp2, err := http.Get(server.URL + "/_plugins/picocss/theme/blue")
	assert.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestPicoTheme_Type(t *testing.T) {
	// Verify PicoTheme is a distinct type
	var theme picocss.PicoTheme = picocss.PicoThemeBlue
	assert.Equal(t, "blue", string(theme))
}

func TestNew_NoOptionsDefaultsToSingleAmber(t *testing.T) {
	// When no themes provided, should default to single Amber theme
	p := picocss.New()
	v := via.New()
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	// Should serve Amber (the default)
	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)

	// Should NOT serve Blue (not in themes list)
	resp2, err := http.Get(server.URL + "/_plugins/picocss/theme/blue")
	assert.NoError(t, err)
	defer resp2.Body.Close()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode)
}

func TestNew_SingleTheme(t *testing.T) {
	p := picocss.New(picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed}))

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestNew_EmptyThemesList_DefaultsToSingleAmber(t *testing.T) {
	// Empty themes list should default to single Amber theme
	p := picocss.New(picocss.WithThemes([]picocss.PicoTheme{}))
	v := via.New()
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	// Should serve Amber (the default when empty)
	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

func TestNew_AllOptions(t *testing.T) {
	p := picocss.New(
		picocss.WithThemes(picocss.AllPicoThemes),
		picocss.WithDefaultTheme(picocss.PicoThemeBlue),
		picocss.WithClassless(),
		picocss.WithColorClasses(),
	)

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestNew_OnlyColorClasses(t *testing.T) {
	p := picocss.New(picocss.WithColorClasses())

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestNew_OnlyClassless(t *testing.T) {
	p := picocss.New(picocss.WithClassless())

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestPicoTheme_StringConversion(t *testing.T) {
	// Test that PicoTheme converts to string correctly
	assert.Equal(t, "amber", string(picocss.PicoThemeAmber))
	assert.Equal(t, "blue", string(picocss.PicoThemeBlue))
	assert.Equal(t, "zinc", string(picocss.PicoThemeZinc))
}

func TestPicoTheme_StringMethod(t *testing.T) {
	// Test the String() method returns the theme name
	assert.Equal(t, "amber", picocss.PicoThemeAmber.String())
	assert.Equal(t, "blue", picocss.PicoThemeBlue.String())
	assert.Equal(t, "purple", picocss.PicoThemePurple.String())
	assert.Equal(t, "red", picocss.PicoThemeRed.String())
	assert.Equal(t, "zinc", picocss.PicoThemeZinc.String())
}

func TestAllPicoThemes_Count(t *testing.T) {
	assert.Equal(t, 19, len(picocss.AllPicoThemes))
}

func TestAllPicoThemes_NoDuplicates(t *testing.T) {
	seen := make(map[string]bool)
	for _, theme := range picocss.AllPicoThemes {
		assert.False(t, seen[string(theme)], "duplicate theme found: %s", theme)
		seen[string(theme)] = true
	}
}

func TestNew_DifferentDefaultThemes(t *testing.T) {
	testCases := []picocss.PicoTheme{
		picocss.PicoThemeAmber,
		picocss.PicoThemeBlue,
		picocss.PicoThemePurple,
		picocss.PicoThemeRed,
		picocss.PicoThemeGreen,
	}

	for _, theme := range testCases {
		t.Run(string(theme), func(t *testing.T) {
			p := picocss.New(picocss.WithDefaultTheme(theme))
			assert.NotNil(t, p)
			assert.Implements(t, (*via.Plugin)(nil), p)
		})
	}
}

func TestNew_RepeatedOptions(t *testing.T) {
	// Last option should win for same option type
	p := picocss.New(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeRed}),
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemeGreen}),
	)

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestPlugin_ServesDefaultTheme(t *testing.T) {
	p := picocss.New()
	v := via.New()
	p.Register(v)

	// Start server
	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	// Request default theme
	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "text/css", resp.Header.Get("Content-Type"))
}

func TestPlugin_InvalidThemeReturns404(t *testing.T) {
	p := picocss.New()
	v := via.New()
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	// Request theme not in list
	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/nonexistent")
	assert.NoError(t, err)
	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

func TestPlugin_WithThemes_ServesOnlyConfiguredThemes(t *testing.T) {
	p := picocss.New(picocss.WithThemes([]picocss.PicoTheme{
		picocss.PicoThemeBlue,
		picocss.PicoThemeRed,
	}))
	v := via.New()
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	// Blue should work
	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/blue")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Red should work
	resp, err = http.Get(server.URL + "/_plugins/picocss/theme/red")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Amber should NOT work (not in themes list)
	resp, err = http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestNew_RepeatedThemes_Deduplicated(t *testing.T) {
	// Repeated themes should be silently deduplicated
	p := picocss.New(picocss.WithThemes([]picocss.PicoTheme{
		picocss.PicoThemeBlue,
		picocss.PicoThemeBlue,
		picocss.PicoThemeRed,
		picocss.PicoThemeBlue,
		picocss.PicoThemeGreen,
		picocss.PicoThemeRed,
	}))
	v := via.New()
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	// Blue should work (first occurrence preserved)
	resp, err := http.Get(server.URL + "/_plugins/picocss/theme/blue")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Red should work
	resp, err = http.Get(server.URL + "/_plugins/picocss/theme/red")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Green should work
	resp, err = http.Get(server.URL + "/_plugins/picocss/theme/green")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusOK, resp.StatusCode)
	resp.Body.Close()

	// Amber should NOT work (not in deduplicated list)
	resp, err = http.Get(server.URL + "/_plugins/picocss/theme/amber")
	assert.NoError(t, err)
	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
	resp.Body.Close()
}

func TestNew_DuplicateThemesWithDefaultInDuplicates(t *testing.T) {
	// Default theme that's duplicated should still be valid
	p := picocss.New(
		picocss.WithThemes([]picocss.PicoTheme{
			picocss.PicoThemeBlue,
			picocss.PicoThemeBlue,
			picocss.PicoThemeRed,
		}),
		picocss.WithDefaultTheme(picocss.PicoThemeBlue),
	)

	assert.NotNil(t, p)
	assert.Implements(t, (*via.Plugin)(nil), p)
}

func TestPlugin_InitializesSignals(t *testing.T) {
	p := picocss.New(
		picocss.WithDefaultTheme(picocss.PicoThemeBlue),
		picocss.WithDarkmodeEnabled(),
	)
	v := via.New()
	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.Div(h.Text("test"))
		})
	})
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	assert.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	assert.Contains(t, string(body), `data-signals`)
	assert.Contains(t, string(body), `_picoTheme`)
	assert.Contains(t, string(body), `_picoDarkMode`)
}

func TestPlugin_DefaultThemeInSignal(t *testing.T) {
	p := picocss.New(
		picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemePurple, picocss.PicoThemeAmber}),
		picocss.WithDefaultTheme(picocss.PicoThemePurple),
	)
	v := via.New()
	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.Div(h.Text("test"))
		})
	})
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	assert.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	assert.Contains(t, string(body), `_picoTheme`)
	assert.Contains(t, string(body), `purple`)
}

func TestPlugin_BindsDataThemeToDarkModeSignal(t *testing.T) {
	p := picocss.New(picocss.WithDarkmodeEnabled())
	v := via.New()
	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.Div(h.Text("test"))
		})
	})
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	assert.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	assert.Contains(t, string(body), `data-attr:data-theme`)
	assert.Contains(t, string(body), `$_picoDarkMode`)
	assert.Contains(t, string(body), `dark`)
	assert.Contains(t, string(body), `white`)
}

func TestPlugin_WithDarkmodeEnabled_HasSignalAndDataTheme(t *testing.T) {
	p := picocss.New(picocss.WithDarkmodeEnabled())
	v := via.New()
	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.Div(h.Text("test"))
		})
	})
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	assert.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	assert.Contains(t, string(body), `$_picoDarkMode`)
	assert.Contains(t, string(body), `data-attr:data-theme`)
}

func TestPlugin_WithDarkmodeEnabled_SignalDefaultTrue(t *testing.T) {
	p := picocss.New(picocss.WithDarkmodeEnabled())
	v := via.New()
	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.Div(h.Text("test"))
		})
	})
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	assert.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	assert.Contains(t, string(body), `_picoDarkMode`)
	assert.Contains(t, string(body), `true`)
}

func TestPlugin_DefaultDarkmodeOff(t *testing.T) {
	p := picocss.New()
	v := via.New()
	v.Page("/", func(c *via.Composition) {
		c.View(func(ctx *via.Context) h.H {
			return h.Div(h.Text("test"))
		})
	})
	p.Register(v)

	server := httptest.NewServer(v.HTTPServeMux())
	defer server.Close()

	resp, err := http.Get(server.URL + "/")
	assert.NoError(t, err)
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	assert.NoError(t, err)

	assert.Contains(t, string(body), `$_picoDarkMode`)
	assert.Contains(t, string(body), `false`)
	assert.Contains(t, string(body), `data-attr:data-theme`)
}
