// Package picocss provides a PicoCSS plugin for the Via framework.
//
// # Quick Start
//
//	v := via.New()
//	plugin := picocss.New(
//	    picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemePurple}),
//	    picocss.WithDefaultTheme(picocss.PicoThemeBlue),
//	    picocss.WithColorClasses(),
//	)
//	plugin.Register(v)
//
// # Changing Theme
//
// In your page view, change the $_picoTheme signal to switch colors:
//
//	h.Button(
//	    h.Text("Purple Theme"),
//	    h.DataOnClick("$_picoTheme = '%s'", picocss.PicoThemePurple.String()),
//	)
//
// # Dark Mode
//
// Toggle the $_picoDarkMode signal to switch between light/dark:
//
//	h.Button(
//	    h.Text("Toggle Dark Mode"),
//	    h.DataOnClick("%s = !%s", "$_picoDarkMode", "$_picoDarkMode"),
//	)
//
// The data-theme attribute on <html> is automatically bound to $_picoDarkMode.
package picocss

import (
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// CDN configuration
const (
	cdnVersion = "2.1.1"
	cdnBase    = "https://cdn.jsdelivr.net/npm/@picocss/pico@" + cdnVersion + "/css/"
)

const (
	cdnThemeURL          = cdnBase + "pico.%s.min.css"
	cdnClasslessThemeURL = cdnBase + "pico.classless.%s.min.css"
	cdnColorClassesURL   = cdnBase + "pico.colors.min.css"
)

// Plugin paths
const (
	pluginPathPrefix   = "/_plugins/picocss/"
	themePath          = pluginPathPrefix + "theme/"
	themeClasslessPath = themePath + "classless/"
	colorClassesPath   = pluginPathPrefix + "color-classes"
)

// PicoTheme represents a Pico CSS color theme.
// Use the predefined constants like PicoThemeBlue, PicoThemePurple, etc.
type PicoTheme string

// Predefined Pico CSS themes. Use these with WithThemes() and WithDefaultTheme().
const (
	PicoThemeAmber   PicoTheme = "amber"
	PicoThemeBlue    PicoTheme = "blue"
	PicoThemeCyan    PicoTheme = "cyan"
	PicoThemeFuchsia PicoTheme = "fuchsia"
	PicoThemeGreen   PicoTheme = "green"
	PicoThemeGrey    PicoTheme = "grey"
	PicoThemeIndigo  PicoTheme = "indigo"
	PicoThemeJade    PicoTheme = "jade"
	PicoThemeLime    PicoTheme = "lime"
	PicoThemeOrange  PicoTheme = "orange"
	PicoThemePink    PicoTheme = "pink"
	PicoThemePumpkin PicoTheme = "pumpkin"
	PicoThemePurple  PicoTheme = "purple"
	PicoThemeRed     PicoTheme = "red"
	PicoThemeSand    PicoTheme = "sand"
	PicoThemeSlate   PicoTheme = "slate"
	PicoThemeViolet  PicoTheme = "violet"
	PicoThemeYellow  PicoTheme = "yellow"
	PicoThemeZinc    PicoTheme = "zinc"
)

// AllPicoThemes contains all 19 available themes.
// Use this with WithThemes() to enable all themes.
var AllPicoThemes = []PicoTheme{
	PicoThemeAmber, PicoThemeBlue, PicoThemeCyan, PicoThemeFuchsia, PicoThemeGreen,
	PicoThemeGrey, PicoThemeIndigo, PicoThemeJade, PicoThemeLime, PicoThemeOrange,
	PicoThemePink, PicoThemePumpkin, PicoThemePurple, PicoThemeRed, PicoThemeSand,
	PicoThemeSlate, PicoThemeViolet, PicoThemeYellow, PicoThemeZinc,
}

// String returns the theme name as a string (e.g., "blue", "purple").
func (t PicoTheme) String() string {
	return string(t)
}

type PicoOption interface {
	apply(*plugin)
}

type withThemesOpt struct {
	themes []PicoTheme
}

func (opt *withThemesOpt) apply(p *plugin) {
	p.opts.themes = opt.themes
}

// WithThemes sets which themes are available to users.
// If not specified, defaults to a single theme: PicoThemeAmber.
// Duplicates are automatically removed. Use AllPicoThemes to enable all 19 themes.
//
// Example:
//
//	picocss.New(picocss.WithThemes([]picocss.PicoTheme{
//	    picocss.PicoThemeBlue,
//	    picocss.PicoThemePurple,
//	    picocss.PicoThemeRed,
//	}))
func WithThemes(themes []PicoTheme) PicoOption {
	return &withThemesOpt{themes: themes}
}

type withDefaultThemeOpt struct {
	defaultTheme PicoTheme
}

func (opt *withDefaultThemeOpt) apply(p *plugin) {
	p.opts.defaultTheme = opt.defaultTheme
}

// WithDefaultTheme sets the initial theme shown when the page loads.
// If the specified theme is not in the themes list, it falls back to the first theme.
// If not specified, defaults to PicoThemeAmber.
//
// Example:
//
//	picocss.New(picocss.WithDefaultTheme(picocss.PicoThemePurple))
func WithDefaultTheme(theme PicoTheme) PicoOption {
	return &withDefaultThemeOpt{defaultTheme: theme}
}

type withClasslessOpt struct{}

func (opt *withClasslessOpt) apply(p *plugin) {
	p.opts.classless = true
}

// WithClassless enables classless Pico CSS mode.
// Classless mode styles HTML elements directly without requiring CSS classes.
// This is useful for rapid prototyping or simple pages.
//
// Example:
//
//	picocss.New(picocss.WithClassless())
func WithClassless() PicoOption {
	return &withClasslessOpt{}
}

type withColorClassesOpt struct{}

func (opt *withColorClassesOpt) apply(p *plugin) {
	p.opts.colorClasses = true
}

// WithColorClasses enables Pico CSS color utility classes.
// When enabled, you can use classes like pico-background-blue, pico-color-red, etc.
// See: https://picocss.com/docs/colors
//
// Example:
//
//	picocss.New(picocss.WithColorClasses())
//
// Then in your HTML:
//
//	h.Div(h.Class("pico-background-purple"), h.Text("Purple background"))
func WithColorClasses() PicoOption {
	return &withColorClassesOpt{}
}

// WithDarkmodeEnabled starts the app in dark mode.
// The $_picoDarkMode signal will be initialized to true.
// Without this option, dark mode starts off (signal = false).
//
// Example:
//
//	picocss.New(picocss.WithDarkmodeEnabled()) // start in dark mode
func WithDarkmodeEnabled() PicoOption {
	return &withDarkmodeEnabledOpt{}
}

type withDarkmodeEnabledOpt struct{}

func (opt *withDarkmodeEnabledOpt) apply(p *plugin) {
	p.opts.darkmodeDefault = true
}

type pluginOptions struct {
	themes          []PicoTheme
	defaultTheme    PicoTheme
	classless       bool
	colorClasses    bool
	darkmodeDefault bool
}

type plugin struct {
	opts             pluginOptions
	themeStylesheets map[PicoTheme][]byte
	colorClassesCSS  []byte
}

// New creates a new PicoCSS plugin with the given options.
//
// Default configuration (no options):
//   - Themes: [PicoThemeAmber] (single amber theme)
//   - Default theme: PicoThemeAmber
//   - Classless mode: disabled
//   - Color classes: disabled
//
// The plugin fetches CSS from the Pico CSS CDN during registration.
// It serves themes at /_plugins/picocss/theme/{theme-name}.
//
// Example usage:
//
//	plugin := picocss.New(
//	    picocss.WithThemes(picocss.AllPicoThemes),
//	    picocss.WithDefaultTheme(picocss.PicoThemeBlue),
//	    picocss.WithColorClasses(),
//	)
//	plugin.Register(v) // v is your Via app
func New(opts ...PicoOption) via.Plugin {
	p := &plugin{
		opts: pluginOptions{
			themes:          []PicoTheme{PicoThemeAmber},
			defaultTheme:    PicoThemeAmber,
			darkmodeDefault: false,
		},
		themeStylesheets: make(map[PicoTheme][]byte),
	}

	// Apply options
	for _, opt := range opts {
		opt.apply(p)
	}

	// Deduplicate themes (preserve order, first occurrence wins)
	p.opts.themes = deduplicate(p.opts.themes)

	// Empty themes (including nil) defaults to single Amber
	if len(p.opts.themes) == 0 {
		p.opts.themes = []PicoTheme{PicoThemeAmber}
	}

	// Invalid default falls back to first in list
	if len(p.opts.themes) > 0 && !slices.Contains(p.opts.themes, p.opts.defaultTheme) {
		p.opts.defaultTheme = p.opts.themes[0]
	}

	return p
}

// deduplicate removes duplicate themes while preserving order
func deduplicate(themes []PicoTheme) []PicoTheme {
	seen := make(map[PicoTheme]bool)
	result := make([]PicoTheme, 0, len(themes))
	for _, t := range themes {
		if !seen[t] {
			seen[t] = true
			result = append(result, t)
		}
	}
	return result
}

func (p *plugin) fetchThemes() error {
	var baseURL string
	if p.opts.classless {
		baseURL = cdnClasslessThemeURL
	} else {
		baseURL = cdnThemeURL
	}

	for _, theme := range p.opts.themes {
		url := fmt.Sprintf(baseURL, theme)
		resp, err := http.Get(url)
		if err != nil {
			return err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			continue
		}

		css, err := io.ReadAll(resp.Body)
		if err != nil {
			return err
		}

		p.themeStylesheets[theme] = css
	}

	// Fetch color classes if enabled
	if p.opts.colorClasses {
		resp, err := http.Get(cdnColorClassesURL)
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				css, _ := io.ReadAll(resp.Body)
				p.colorClassesCSS = css
			}
		}
	}

	return nil
}

func (p *plugin) servePluginAssets(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	// /_plugins/picocss/theme/ or /_plugins/picocss/theme/classless/{theme}
	if strings.HasPrefix(path, themePath) {
		themeStr := strings.TrimPrefix(path, themePath)
		themeStr = strings.TrimPrefix(themeStr, "classless/")

		theme := PicoTheme(themeStr)
		if css, ok := p.themeStylesheets[theme]; ok {
			w.Header().Set("Content-Type", "text/css")
			w.Header().Set("Cache-Control", "public, max-age=86400")
			w.Write(css)
			return
		}
	}

	// /_plugins/picocss/color-classes
	if path == colorClassesPath && p.opts.colorClasses {
		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Write(p.colorClassesCSS)
		return
	}

	http.NotFound(w, r)
}

// Register adds the PicoCSS plugin to the Via app.
func (p *plugin) Register(v *via.V) {
	if err := p.fetchThemes(); err != nil {
		panic("pico: failed to fetch themes: " + err.Error())
	}

	// Initialize signals (theme always enabled, dark mode uses darkmodeDefault)
	v.AppendToHead(h.Meta(h.DataSignals(
		`{"_picoTheme": "%s", "_picoDarkMode": %t}`,
		p.opts.defaultTheme,
		p.opts.darkmodeDefault,
	)))

	// Bind data-theme to dark mode signal
	v.AppendHTMLAttr(h.DataAttr("data-theme", "$_picoDarkMode ? 'dark' : 'white'"))

	// Determine theme URL path
	var themeURL string
	if p.opts.classless {
		themeURL = themeClasslessPath + string(p.opts.defaultTheme)
	} else {
		themeURL = themePath + string(p.opts.defaultTheme)
	}

	v.AppendToHead(h.Link(
		h.Rel("stylesheet"),
		h.Href(themeURL),
		h.DataAttr("href", "'/_plugins/picocss/theme/' + $_picoTheme"),
	))

	if p.opts.colorClasses {
		v.AppendToHead(h.Link(
			h.Rel("stylesheet"),
			h.Href(colorClassesPath),
		))
	}

	// Register HTTP handlers for plugin assets
	v.HTTPServeMux().Handle("GET "+themePath, http.HandlerFunc(p.servePluginAssets))
	if p.opts.colorClasses {
		v.HTTPServeMux().Handle("GET "+colorClassesPath, http.HandlerFunc(p.servePluginAssets))
	}
}
