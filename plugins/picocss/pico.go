// Package picocss provides a PicoCSS plugin for the Via engine.
//
// The theme CSS ships embedded in the binary (vendored from the pinned
// Pico release), so registration does no network I/O and apps boot
// offline. Assets are served at content-hashed /via/assets/picocss/
// paths with immutable cache headers.
//
// Quick start:
//
//	app := via.New(via.WithPlugins(
//	    picocss.Plugin(
//	        picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemePurple}),
//	        picocss.WithDefaultTheme(picocss.PicoThemeBlue),
//	        picocss.WithColorClasses(),
//	    ),
//	))
//
// The plugin registers two app-wide client signals:
//
//   - $_picoTheme — the active theme name (set from any composition: see below)
//   - $_picoDarkMode — "system" (default), "dark", or "light"
//
// Bind a button to the $_picoTheme signal:
//
//	h.Button(
//	    h.Text("Purple Theme"),
//	    h.Data("on-click", fmt.Sprintf("$_picoTheme = '%s'", picocss.PicoThemePurple)),
//	)
package picocss

//go:generate sh refresh_assets.sh

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// Vendored Pico CSS release: 2.1.1. refresh_assets.sh must be re-run (and
// the embedded files re-vendored) whenever it changes.

const (
	pluginPathPrefix = "/_plugins/picocss/"
	themePath        = pluginPathPrefix + "theme/"
	colorClassesPath = pluginPathPrefix + "color-classes"
)

// PicoTheme is a Pico CSS color theme name.
type PicoTheme string

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

// AllPicoThemes lists every supported Pico CSS color theme. Pass to
// WithThemes to enable the full set, or pick a subset.
var AllPicoThemes = []PicoTheme{
	PicoThemeAmber, PicoThemeBlue, PicoThemeCyan, PicoThemeFuchsia, PicoThemeGreen,
	PicoThemeGrey, PicoThemeIndigo, PicoThemeJade, PicoThemeLime, PicoThemeOrange,
	PicoThemePink, PicoThemePumpkin, PicoThemePurple, PicoThemeRed, PicoThemeSand,
	PicoThemeSlate, PicoThemeViolet, PicoThemeYellow, PicoThemeZinc,
}

// String returns the theme name.
func (t PicoTheme) String() string { return string(t) }

// PicoOption configures the plugin. Functional-options shape: each option
// is a closure that mutates the plugin's option struct, applied in order
// by Plugin. Conflicting or duplicate options panic at registration.
type PicoOption func(*plugin)

type pluginOptions struct {
	themes       []PicoTheme
	defaultTheme PicoTheme
	classless    bool
	colorClasses bool
	darkMode     string // "system" | "dark" | "light"

	themesSet       bool
	defaultThemeSet bool
	darkModeSet     bool
}

type plugin struct {
	opts              pluginOptions
	themeAssets       map[PicoTheme]*asset
	colorClassesAsset *asset
	assetsByName      map[string]*asset
}

// Plugin builds a PicoCSS plugin. Defaults: PicoThemeAmber, system dark
// mode. All CSS is loaded from the embedded vendored release — Plugin
// and Register never touch the network. Invalid or conflicting options
// panic here, at registration time.
func Plugin(opts ...PicoOption) via.Plugin {
	p := &plugin{
		opts: pluginOptions{
			themes:       []PicoTheme{PicoThemeAmber},
			defaultTheme: PicoThemeAmber,
			darkMode:     "system",
		},
	}
	for _, opt := range opts {
		opt(p)
	}
	// A lone WithDefaultTheme implies that theme is wanted; requiring a
	// redundant WithThemes([theme]) would be hostile for the common
	// single-theme app.
	if p.opts.defaultThemeSet && !p.opts.themesSet {
		p.opts.themes = []PicoTheme{p.opts.defaultTheme}
	}
	if !p.opts.defaultThemeSet {
		p.opts.defaultTheme = p.opts.themes[0]
	}
	if !slices.Contains(p.opts.themes, p.opts.defaultTheme) {
		panic(fmt.Sprintf(
			"picocss: default theme %q is not in WithThemes(%v) — the initial stylesheet would never load",
			p.opts.defaultTheme, p.opts.themes))
	}

	p.themeAssets = make(map[PicoTheme]*asset, len(p.opts.themes))
	p.assetsByName = make(map[string]*asset, len(p.opts.themes)+1)
	for _, theme := range p.opts.themes {
		a := newAsset(p.themeFile(theme))
		p.themeAssets[theme] = a
		p.assetsByName[a.name] = a
	}
	if p.opts.colorClasses {
		p.colorClassesAsset = newAsset("pico.colors.min.css")
		p.assetsByName[p.colorClassesAsset.name] = p.colorClassesAsset
	}
	return p
}

func (p *plugin) themeFile(theme PicoTheme) string {
	if p.opts.classless {
		return fmt.Sprintf("pico.classless.%s.min.css", theme)
	}
	return fmt.Sprintf("pico.%s.min.css", theme)
}

// WithThemes sets which themes are available. Defaults to
// [PicoThemeAmber]. Panics on an empty list, an unknown theme, a
// duplicate entry, or when set twice.
func WithThemes(themes []PicoTheme) PicoOption {
	return func(p *plugin) {
		if p.opts.themesSet {
			panic("picocss: WithThemes set twice — pass one combined theme list")
		}
		if len(themes) == 0 {
			panic("picocss: WithThemes: theme list cannot be empty")
		}
		seen := make(map[PicoTheme]bool, len(themes))
		for _, theme := range themes {
			if !slices.Contains(AllPicoThemes, theme) {
				panic(fmt.Sprintf("picocss: unknown theme %q — no embedded asset for it", theme))
			}
			if seen[theme] {
				panic(fmt.Sprintf("picocss: duplicate theme %q in WithThemes", theme))
			}
			seen[theme] = true
		}
		p.opts.themes = themes
		p.opts.themesSet = true
	}
}

// WithDefaultTheme sets the initial theme on page load. Panics on an
// unknown theme or when set twice; Plugin panics when the default is
// not among the WithThemes list.
func WithDefaultTheme(theme PicoTheme) PicoOption {
	return func(p *plugin) {
		if p.opts.defaultThemeSet {
			panic("picocss: WithDefaultTheme set twice — conflicting defaults")
		}
		if !slices.Contains(AllPicoThemes, theme) {
			panic(fmt.Sprintf("picocss: unknown theme %q — no embedded asset for it", theme))
		}
		p.opts.defaultTheme = theme
		p.opts.defaultThemeSet = true
	}
}

// WithClassless enables classless Pico CSS mode.
func WithClassless() PicoOption { return func(p *plugin) { p.opts.classless = true } }

// WithColorClasses enables pico-color-* utility classes.
func WithColorClasses() PicoOption { return func(p *plugin) { p.opts.colorClasses = true } }

// WithDarkMode forces dark mode. Panics when combined with
// WithLightMode.
func WithDarkMode() PicoOption {
	return func(p *plugin) {
		if p.opts.darkModeSet {
			panic("picocss: conflicting dark/light mode options")
		}
		p.opts.darkMode = "dark"
		p.opts.darkModeSet = true
	}
}

// WithLightMode forces light mode. Panics when combined with
// WithDarkMode.
func WithLightMode() PicoOption {
	return func(p *plugin) {
		if p.opts.darkModeSet {
			panic("picocss: conflicting dark/light mode options")
		}
		p.opts.darkMode = "light"
		p.opts.darkModeSet = true
	}
}

const darkModeBindExpr = `$_picoDarkMode==='system'` +
	`?(window.matchMedia('(prefers-color-scheme: dark)').matches?'dark':'light')` +
	`:$_picoDarkMode`

func (p *plugin) serveLegacyAssets(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	if strings.HasPrefix(path, themePath) {
		themeStr := strings.TrimPrefix(path, themePath)
		themeStr = strings.TrimPrefix(themeStr, "classless/")
		a, ok := p.themeAssets[PicoTheme(themeStr)]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeCSSAsset(w, r, a)
		return
	}
	if path == colorClassesPath && p.opts.colorClasses {
		writeCSSAsset(w, r, p.colorClassesAsset)
		return
	}
	http.NotFound(w, r)
}

// writeCSSAsset serves a CSS asset at a name-addressed (non-hashed) URL
// with encoding-correct caching: a Vary: Accept-Encoding header so
// shared caches key per encoding, and a representation-specific ETag
// (the gzip variant is suffixed) so a cross-encoding If-None-Match
// can't 304 the wrong body.
func writeCSSAsset(w http.ResponseWriter, r *http.Request, a *asset) {
	w.Header().Set("Vary", "Accept-Encoding")
	gzip := acceptsGzip(r)
	etag := a.hash
	if gzip {
		etag = a.hash + "-gz"
	}
	if r.Header.Get("If-None-Match") == etag {
		w.WriteHeader(http.StatusNotModified)
		return
	}
	w.Header().Set("Content-Type", a.contentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("ETag", etag)
	if gzip {
		w.Header().Set("Content-Encoding", "gzip")
		_, _ = w.Write(a.gz)
		return
	}
	_, _ = w.Write(a.body)
}

const (
	darkModeSignalID = "_picoDarkMode"
	themeSignalID    = "_picoTheme"
)

// ThemeRef returns the Datastar reference for the current theme signal,
// e.g. "$_picoTheme". Use it inline in expressions:
//
//	h.Button(
//	    h.Text("Blue"),
//	    h.Data("on-click", picocss.ThemeRef()+" = 'blue'"),
//	)
func ThemeRef() string { return "$" + themeSignalID }

// DarkModeRef returns the Datastar reference for the dark-mode signal.
func DarkModeRef() string { return "$" + darkModeSignalID }

func (p *plugin) Register(v *via.App) {
	v.RegisterAppSignal(darkModeSignalID, p.opts.darkMode)
	v.RegisterAppSignal(themeSignalID, string(p.opts.defaultTheme))

	v.AppendAttrToHTML(h.Data("attr:data-theme", darkModeBindExpr))

	// Theme URLs are content-hashed, so the client maps theme name to
	// URL instead of concatenating a stable prefix.
	urls := make(map[string]string, len(p.themeAssets))
	for theme, a := range p.themeAssets {
		urls[string(theme)] = a.path()
	}
	urlsJSON, err := json.Marshal(urls)
	if err != nil {
		panic(fmt.Sprintf("picocss: encode theme URL map: %v", err))
	}

	v.AppendToHead(h.Link(
		h.Rel("stylesheet"),
		h.ID("_picoThemeLink"),
		h.Data("attr:href", fmt.Sprintf("(%s)[$_picoTheme]", urlsJSON)),
	))

	v.AppendToHead(h.Script(h.Raw(fmt.Sprintf(`(function(){`+
		`var u=%s;`+
		`var m=document.querySelector('meta[data-signals]');`+
		`if(!m)return;`+
		`try{var s=JSON.parse(m.getAttribute('data-signals'));`+
		`var dm=s._picoDarkMode;`+
		`if(dm==='dark'||dm==='light')document.documentElement.setAttribute('data-theme',dm);`+
		`else if(dm==='system')document.documentElement.setAttribute('data-theme',`+
		`window.matchMedia('(prefers-color-scheme:dark)').matches?'dark':'light');`+
		`var t=s._picoTheme;`+
		`if(t&&u[t])document.getElementById('_picoThemeLink').setAttribute('href',u[t]);`+
		`}catch(e){}})();`, urlsJSON))))

	if p.opts.colorClasses {
		v.AppendToHead(h.Link(
			h.Rel("stylesheet"),
			h.Href(p.colorClassesAsset.path()),
		))
	}

	v.HandleFunc("GET "+assetPathPrefix, p.serveHashedAsset)
	v.HandleFunc("GET "+themePath, p.serveLegacyAssets)
	if p.opts.colorClasses {
		v.HandleFunc("GET "+colorClassesPath, p.serveLegacyAssets)
	}
}
