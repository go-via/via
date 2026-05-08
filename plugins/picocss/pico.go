// Package picocss provides a PicoCSS plugin for the Via engine.
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

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"hash/crc32"
	"io"
	"net/http"
	"slices"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

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

const maxCSSBodySize = 512 * 1024

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

var AllPicoThemes = []PicoTheme{
	PicoThemeAmber, PicoThemeBlue, PicoThemeCyan, PicoThemeFuchsia, PicoThemeGreen,
	PicoThemeGrey, PicoThemeIndigo, PicoThemeJade, PicoThemeLime, PicoThemeOrange,
	PicoThemePink, PicoThemePumpkin, PicoThemePurple, PicoThemeRed, PicoThemeSand,
	PicoThemeSlate, PicoThemeViolet, PicoThemeYellow, PicoThemeZinc,
}

// String returns the theme name.
func (t PicoTheme) String() string { return string(t) }

// PicoOption configures the plugin.
type PicoOption interface{ apply(*plugin) }

type pluginOptions struct {
	themes       []PicoTheme
	defaultTheme PicoTheme
	classless    bool
	colorClasses bool
	darkMode     string // "system" | "dark" | "light"
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

// Plugin builds a PicoCSS plugin. Defaults: PicoThemeAmber, system dark mode.
func Plugin(opts ...PicoOption) via.Plugin {
	p := &plugin{
		opts: pluginOptions{
			themes:       []PicoTheme{PicoThemeAmber},
			defaultTheme: PicoThemeAmber,
			darkMode:     "system",
		},
		themeCSS:     make(map[PicoTheme][]byte),
		themeCSSGzip: make(map[PicoTheme][]byte),
		themeETags:   make(map[PicoTheme]string),
	}
	for _, opt := range opts {
		opt.apply(p)
	}
	p.opts.themes = dedup(p.opts.themes)
	if len(p.opts.themes) == 0 {
		p.opts.themes = []PicoTheme{PicoThemeAmber}
	}
	if !slices.Contains(p.opts.themes, p.opts.defaultTheme) {
		p.opts.defaultTheme = p.opts.themes[0]
	}
	return p
}

type withThemesOpt struct{ themes []PicoTheme }

func (o *withThemesOpt) apply(p *plugin) { p.opts.themes = o.themes }

// WithThemes sets which themes are available. Defaults to [PicoThemeAmber].
func WithThemes(themes []PicoTheme) PicoOption { return &withThemesOpt{themes: themes} }

type withDefaultThemeOpt struct{ theme PicoTheme }

func (o *withDefaultThemeOpt) apply(p *plugin) { p.opts.defaultTheme = o.theme }

// WithDefaultTheme sets the initial theme on page load.
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

// WithDarkMode forces dark mode.
func WithDarkMode() PicoOption { return &withDarkModeOpt{mode: "dark"} }

// WithLightMode forces light mode.
func WithLightMode() PicoOption { return &withDarkModeOpt{mode: "light"} }

// Helpers

func dedup(themes []PicoTheme) []PicoTheme {
	seen := make(map[PicoTheme]bool, len(themes))
	out := make([]PicoTheme, 0, len(themes))
	for _, t := range themes {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
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

func crc32Hex(b []byte) string { return fmt.Sprintf("%08x", crc32.ChecksumIEEE(b)) }

func gzipBytes(b []byte) []byte {
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	_, _ = w.Write(b)
	_ = w.Close()
	return buf.Bytes()
}

func acceptsGzip(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept-Encoding"), "gzip")
}

const darkModeBindExpr = `$_picoDarkMode==='system'` +
	`?(window.matchMedia('(prefers-color-scheme: dark)').matches?'dark':'light')` +
	`:$_picoDarkMode`

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
			_, _ = w.Write(p.themeCSSGzip[theme])
		} else {
			_, _ = w.Write(css)
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
			_, _ = w.Write(p.colorClassesCSSGzip)
		} else {
			_, _ = w.Write(p.colorClassesCSS)
		}
		return
	}
	http.NotFound(w, r)
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
	if err := p.fetchThemes(); err != nil {
		panic("pico: failed to fetch themes: " + err.Error())
	}

	v.RegisterAppSignal(darkModeSignalID, p.opts.darkMode)
	v.RegisterAppSignal(themeSignalID, string(p.opts.defaultTheme))

	v.AppendAttrToHTML(h.Data("attr:data-theme", darkModeBindExpr))

	themePrefix := themePath
	if p.opts.classless {
		themePrefix = themePath + "classless/"
	}

	v.AppendToHead(h.Link(
		h.Rel("stylesheet"),
		h.ID("_picoThemeLink"),
		h.Data("attr:href", fmt.Sprintf("'%s'+$_picoTheme", themePrefix)),
	))

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
