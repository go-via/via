// Package picocss provides a PicoCSS plugin for the Via engine.
//
// # Quick Start
//
//	app := via.New(via.WithPlugins(
//	    picocss.Plugin(
//	        picocss.WithThemes([]picocss.PicoTheme{picocss.PicoThemeBlue, picocss.PicoThemePurple}),
//	        picocss.WithDefaultTheme(picocss.PicoThemeBlue),
//	        picocss.WithColorClasses(),
//	    ),
//	))
//
// # Changing Theme
//
// Bind a button to the $_picoTheme signal:
//
//	h.Button(
//	    h.Text("Purple Theme"),
//	    h.Data("on-click", fmt.Sprintf("$_picoTheme = '%s'", picocss.PicoThemePurple)),
//	)
//
// # Dark Mode
//
// $_picoDarkMode accepts "system", "dark", or "light". "system" resolves to
// the browser's prefers-color-scheme at runtime. Use WithDarkMode() or
// WithLightMode() to override the default ("system").
//
//	h.Select(
//	    h.Option(h.Value("system"), h.Text("System")),
//	    h.Option(h.Value("dark"),   h.Text("Dark")),
//	    h.Option(h.Value("light"),  h.Text("Light")),
//	    h.Data("model", "$_picoDarkMode"),
//	)
package picocss

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

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

const (
	pluginPathPrefix = "/_plugins/picocss/"
	themePath        = pluginPathPrefix + "theme/"
	colorClassesPath = pluginPathPrefix + "color-classes"
)

// maxCSSBodySize caps CDN response bodies to prevent excessive memory use.
const maxCSSBodySize = 512 * 1024

// PicoTheme represents a Pico CSS color theme.
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
var AllPicoThemes = []PicoTheme{
	PicoThemeAmber, PicoThemeBlue, PicoThemeCyan, PicoThemeFuchsia, PicoThemeGreen,
	PicoThemeGrey, PicoThemeIndigo, PicoThemeJade, PicoThemeLime, PicoThemeOrange,
	PicoThemePink, PicoThemePumpkin, PicoThemePurple, PicoThemeRed, PicoThemeSand,
	PicoThemeSlate, PicoThemeViolet, PicoThemeYellow, PicoThemeZinc,
}

// String returns the theme name as a string (e.g., "blue").
func (t PicoTheme) String() string { return string(t) }

// PicoOption configures the PicoCSS plugin.
type PicoOption interface {
	apply(*plugin)
}

type pluginOptions struct {
	themes       []PicoTheme
	defaultTheme PicoTheme
	classless    bool
	colorClasses bool
	darkMode string // "system" (default), "dark", or "light"
}

type plugin struct {
	opts                pluginOptions
	themeCSS            map[PicoTheme][]byte
	themeCSSGzip        map[PicoTheme][]byte
	themeETags          map[PicoTheme]string
	colorClassesCSS     []byte
	colorClassesCSSGzip []byte
	colorClassesETag    string
}

// Plugin creates a PicoCSS plugin with the given options.
//
// Default configuration (no options):
//   - Themes: [PicoThemeAmber]
//   - Default theme: PicoThemeAmber
//   - Dark mode: system preference (prefers-color-scheme)
//   - Classless: disabled
//   - Color classes: disabled
func Plugin(opts ...PicoOption) via.Plugin {
	p := &plugin{
		opts: pluginOptions{
			themes:       []PicoTheme{PicoThemeAmber},
			defaultTheme: PicoThemeAmber,
			darkMode: "system",
		},
		themeCSS:     make(map[PicoTheme][]byte),
		themeCSSGzip: make(map[PicoTheme][]byte),
		themeETags:   make(map[PicoTheme]string),
	}
	for _, opt := range opts {
		opt.apply(p)
	}
	p.opts.themes = deduplicate(p.opts.themes)
	if len(p.opts.themes) == 0 {
		p.opts.themes = []PicoTheme{PicoThemeAmber}
	}
	if !slices.Contains(p.opts.themes, p.opts.defaultTheme) {
		p.opts.defaultTheme = p.opts.themes[0]
	}
	return p
}

// --- Options ---

type withThemesOpt struct{ themes []PicoTheme }

func (o *withThemesOpt) apply(p *plugin) { p.opts.themes = o.themes }

// WithThemes sets which themes are available. Defaults to [PicoThemeAmber].
// Duplicates are removed. Use AllPicoThemes to enable all 19 themes.
func WithThemes(themes []PicoTheme) PicoOption { return &withThemesOpt{themes: themes} }

type withDefaultThemeOpt struct{ theme PicoTheme }

func (o *withDefaultThemeOpt) apply(p *plugin) { p.opts.defaultTheme = o.theme }

// WithDefaultTheme sets the initial theme on page load.
// Falls back to the first theme if not in the themes list.
func WithDefaultTheme(theme PicoTheme) PicoOption { return &withDefaultThemeOpt{theme: theme} }

type withClasslessOpt struct{}

func (o *withClasslessOpt) apply(p *plugin) { p.opts.classless = true }

// WithClassless enables classless Pico CSS mode.
func WithClassless() PicoOption { return &withClasslessOpt{} }

type withColorClassesOpt struct{}

func (o *withColorClassesOpt) apply(p *plugin) { p.opts.colorClasses = true }

// WithColorClasses enables pico-color-* utility classes.
func WithColorClasses() PicoOption { return &withColorClassesOpt{} }

type withDarkModeOpt struct{ mode string }

func (o *withDarkModeOpt) apply(p *plugin) { p.opts.darkMode = o.mode }

// WithDarkMode forces dark mode on ($_picoDarkMode = "dark").
func WithDarkMode() PicoOption { return &withDarkModeOpt{mode: "dark"} }

// WithLightMode forces light mode on ($_picoDarkMode = "light").
func WithLightMode() PicoOption { return &withDarkModeOpt{mode: "light"} }

// --- Helpers ---

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

func fetchCSS(url string) ([]byte, error) {
	resp, err := http.Get(url) //nolint:gosec
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("pico: fetch %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, maxCSSBodySize))
}

func crc32Hex(b []byte) string {
	return fmt.Sprintf("%08x", crc32.ChecksumIEEE(b))
}

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	w.Write(b)
	w.Close()
	return buf.Bytes()
}

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

// darkModeBindExpr returns the Datastar expression that resolves $_picoDarkMode
// to a data-theme value. "system" evaluates the browser's prefers-color-scheme
// at runtime; "dark" and "light" are used as-is.
const darkModeBindExpr = `$_picoDarkMode==='system'` +
	`?(window.matchMedia('(prefers-color-scheme: dark)').matches?'dark':'light')` +
	`:$_picoDarkMode`

// --- Fetch ---

type fetchResult struct {
	theme PicoTheme
	css   []byte
	err   error
}

func (p *plugin) fetchThemes() error {
	baseURL := cdnThemeURL
	if p.opts.classless {
		baseURL = cdnClasslessThemeURL
	}

	ch := make(chan fetchResult, len(p.opts.themes))
	for _, theme := range p.opts.themes {
		theme := theme
		go func() {
			css, err := fetchCSS(fmt.Sprintf(baseURL, theme))
			ch <- fetchResult{theme: theme, css: css, err: err}
		}()
	}

	for range p.opts.themes {
		r := <-ch
		if r.err != nil {
			return r.err
		}
		p.themeCSS[r.theme] = r.css
		p.themeETags[r.theme] = crc32Hex(r.css)
		p.themeCSSGzip[r.theme] = gzipBytes(r.css)
	}

	if p.opts.colorClasses {
		css, err := fetchCSS(cdnColorClassesURL)
		if err != nil {
			return err
		}
		p.colorClassesCSS = css
		p.colorClassesETag = crc32Hex(css)
		p.colorClassesCSSGzip = gzipBytes(css)
	}

	return nil
}

// --- HTTP handler ---

func (p *plugin) servePluginAssets(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	if strings.HasPrefix(path, themePath) {
		themeStr := strings.TrimPrefix(path, themePath)
		themeStr = strings.TrimPrefix(themeStr, "classless/")
		theme := PicoTheme(themeStr)

		css, ok := p.themeCSS[theme]
		if !ok {
			http.NotFound(w, r)
			return
		}

		etag := p.themeETags[theme]
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("ETag", etag)
		if acceptsGzip(r) {
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(p.themeCSSGzip[theme])
		} else {
			w.Write(css)
		}
		return
	}

	if path == colorClassesPath && p.opts.colorClasses {
		etag := p.colorClassesETag
		if r.Header.Get("If-None-Match") == etag {
			w.WriteHeader(http.StatusNotModified)
			return
		}

		w.Header().Set("Content-Type", "text/css")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("ETag", etag)
		if acceptsGzip(r) {
			w.Header().Set("Content-Encoding", "gzip")
			w.Write(p.colorClassesCSSGzip)
		} else {
			w.Write(p.colorClassesCSS)
		}
		return
	}

	http.NotFound(w, r)
}

const (
	darkModeSignalID = "_picoDarkMode"
	themeSignalID    = "_picoTheme"
)

var (
	sigDarkMode atomic.Pointer[via.AppSignalHandle[string]]
	sigTheme    atomic.Pointer[via.AppSignalHandle[string]]
)

// ThemeSig returns the app-level theme signal handle.
func ThemeSig() *via.AppSignalHandle[string] { return sigTheme.Load() }

// DarkModeSig returns the app-level dark mode signal handle.
func DarkModeSig() *via.AppSignalHandle[string] { return sigDarkMode.Load() }

// --- Register ---

func (p *plugin) Register(v *via.App) {
	if err := p.fetchThemes(); err != nil {
		panic("pico: failed to fetch themes: " + err.Error())
	}

	sigDarkMode.Store(via.AppSignal(v, darkModeSignalID, p.opts.darkMode))
	sigTheme.Store(via.AppSignal(v, themeSignalID, string(p.opts.defaultTheme)))

	// Reactively bind data-theme on <html> to the dark mode signal.
	v.AppendAttrToHTML(h.Data("attr:data-theme", darkModeBindExpr))

	themePrefix := themePath
	if p.opts.classless {
		themePrefix = themePath + "classless/"
	}

	// Stylesheet with no static href — the blocking script below sets it.
	v.AppendToHead(h.Link(
		h.Rel("stylesheet"),
		h.ID("_picoThemeLink"),
		h.Data("attr:href", fmt.Sprintf("'%s'+$_picoTheme", themePrefix)),
	))

	// Blocking script: runs after the <link> is in the DOM but before the
	// browser renders. Reads signals from the preceding <meta>, resolves
	// dark mode and theme, and sets both data-theme and the stylesheet href
	// synchronously — zero flash.
	v.AppendToHead(h.Script(h.Raw(fmt.Sprintf(`(function(){`+
		`var m=document.querySelector('meta[data-signals]');`+
		`if(!m)return;`+
		`try{var s=JSON.parse(m.getAttribute('data-signals'));`+
		`var dm=s._picoDarkMode;`+
		`if(dm==='dark'||dm==='light')document.documentElement.setAttribute('data-theme',dm);`+
		`else if(dm==='system')document.documentElement.setAttribute('data-theme',`+
		`window.matchMedia('(prefers-color-scheme:dark)').matches?'dark':'light');`+
		`var t=s._picoTheme;`+
		`if(t)document.getElementById('_picoThemeLink').setAttribute('href','%s'+t);`+
		`}catch(e){}})();`, themePrefix))))

	if p.opts.colorClasses {
		v.AppendToHead(h.Link(
			h.Rel("stylesheet"),
			h.Href(colorClassesPath),
		))
	}

	v.HandleFunc("GET "+themePath, p.servePluginAssets)
	if p.opts.colorClasses {
		v.HandleFunc("GET "+colorClassesPath, p.servePluginAssets)
	}
}
