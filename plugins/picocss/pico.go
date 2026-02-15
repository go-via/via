// Package picocss provides a PicoCSS plugin for the Via framework.
//
// # Quick Start
//
//	v := via.New()
//	plugin := picocss.New(picocss.Options{
//	    Themes:       picocss.AllThemes,
//	    DefaultTheme: "blue",
//	    ColorClasses: true,
//	    DarkMode:     true,
//	})
//	plugin.Register(v)
//
// # Changing Theme
//
// In your page view, change the $_picoTheme signal to switch colors:
//
//	h.Button(
//	    h.Text("Purple Theme"),
//	    h.DataOnClick("$_picoTheme = 'purple'"),
//	)
//
// # Dark Mode
//
// Toggle the $_picoDarkMode signal to switch between light/dark:
//
//	h.Button(
//	    h.Text("Toggle Dark Mode"),
//	    h.DataOnClick("$_picoDarkMode = !$_picoDarkMode"),
//	)
//
// The data-theme attribute on <html> is automatically bound to $_picoDarkMode.
package picocss

import (
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

var AllThemes = []string{
	"amber", "blue", "cyan", "fuchsia", "green",
	"grey", "indigo", "jade", "lime", "orange", "pink",
	"pumpkin", "purple", "red", "sand", "slate", "violet", "yellow", "zinc",
}

// Options configures the PicoCSS plugin.
//
// Themes: List of available theme names (default: AllThemes)
// DefaultTheme: Initial theme on page load (default: "blue")
// Classless: Use classless PicoCSS version (default: false)
// ColorClasses: Enable pico-background-COLOR utility classes (default: false)
// DarkMode: Enable dark/light mode toggle with $_picoDarkMode signal (default: false)
type Options struct {
	Themes       []string
	DefaultTheme string
	Classless    bool
	ColorClasses bool
	DarkMode     bool
}

// Plugin wraps PicoCSS configuration and serves theme CSS.
type Plugin struct {
	opts         Options
	themes       []string
	themeCSS     map[string]string
	themeURL     string
	colorClasses string
	HeadLink     h.H
}

// New creates a new PicoCSS plugin with the given options.
func New(opts Options) *Plugin {
	if opts.DefaultTheme == "" {
		opts.DefaultTheme = "blue"
	}
	if len(opts.Themes) == 0 {
		opts.Themes = AllThemes
	}

	p := &Plugin{
		opts:         opts,
		themes:       opts.Themes,
		themeCSS:     make(map[string]string),
		themeURL:     "/_pico/theme/",
		colorClasses: "",
	}

	validDefault := false
	for _, t := range p.themes {
		if t == p.opts.DefaultTheme {
			validDefault = true
			break
		}
	}
	if !validDefault {
		p.opts.DefaultTheme = p.themes[0]
	}

	themePath := p.opts.DefaultTheme
	if p.opts.Classless {
		themePath = "classless/" + themePath
	}

	p.HeadLink = h.Link(
		h.ID("pico-theme"),
		h.Rel("stylesheet"),
		h.Href("/_pico/theme/"+themePath),
		h.Attr("data-attr:href", "'/_pico/theme/' + $_picoTheme"),
	)

	return p
}

func (p *Plugin) FetchThemes() error {
	var baseURL string
	if p.opts.Classless {
		baseURL = "https://cdn.jsdelivr.net/npm/@picocss/pico@2.1.1/css/pico.classless.%s.min.css"
	} else {
		baseURL = "https://cdn.jsdelivr.net/npm/@picocss/pico@2.1.1/css/pico.%s.min.css"
	}

	for _, theme := range p.themes {
		url := strings.ReplaceAll(baseURL, "%s", theme)
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

		p.themeCSS[theme] = string(css)
	}

	// Fetch color classes if enabled
	if p.opts.ColorClasses {
		resp, err := http.Get("https://cdn.jsdelivr.net/npm/@picocss/pico@2.1.1/css/pico.colors.min.css")
		if err == nil {
			defer resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				css, _ := io.ReadAll(resp.Body)
				p.colorClasses = string(css)
			}
		}
	}

	return nil
}

func (p *Plugin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	theme := strings.TrimPrefix(r.URL.Path, "/_pico/theme/")
	if theme == "" {
		http.NotFound(w, r)
		return
	}

	// Handle classless themes: "classless/blue" -> "blue"
	if strings.HasPrefix(theme, "classless/") {
		theme = strings.TrimPrefix(theme, "classless/")
	}

	css, ok := p.themeCSS[theme]
	if !ok {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", "text/css")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(css))
}

// Register adds the PicoCSS plugin to the Via app.
func (p *Plugin) Register(v *via.V) {
	if err := p.FetchThemes(); err != nil {
		panic("pico: failed to fetch themes: " + err.Error())
	}

	v.AppendToHead(p.HeadLink)

	v.HTTPServeMux().Handle("GET /_pico/theme/", http.HandlerFunc(p.ServeHTTP))

	if p.opts.ColorClasses {
		v.AppendToHead(p.ColorClassesLink())
		v.HTTPServeMux().Handle("GET /_pico/color-classes", http.HandlerFunc(p.ServeColorClasses))
	}

	if p.opts.DarkMode {
		v.AppendToHead(h.Div(h.Data("signals", "{_picoDarkMode: true}")))
		v.AppendHTMLAttr(h.Attr("data-attr:data-theme", "$_picoDarkMode ? 'dark' : 'light'"))
	}
}

func (p *Plugin) ServeColorClasses(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/css")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Write([]byte(p.colorClasses))
}

func (p *Plugin) ColorClassesLink() h.H {
	if p.colorClasses == "" {
		return nil
	}
	return h.Link(
		h.ID("pico-color-classes"),
		h.Rel("stylesheet"),
		h.Href("/_pico/color-classes"),
	)
}

type ThemeHandle struct {
	signalName string
	opts       Options
}

func Theme(c *via.Composition, opts Options) *ThemeHandle {
	if opts.DefaultTheme == "" {
		opts.DefaultTheme = "blue"
	}
	if len(opts.Themes) == 0 {
		opts.Themes = AllThemes
	}

	validDefault := false
	for _, t := range opts.Themes {
		if t == opts.DefaultTheme {
			validDefault = true
			break
		}
	}
	if !validDefault {
		opts.DefaultTheme = opts.Themes[0]
	}
	return &ThemeHandle{
		signalName: "_picoTheme",
		opts:       opts,
	}
}

func (th *ThemeHandle) Link() h.H {
	themePath := th.opts.DefaultTheme
	if th.opts.Classless {
		themePath = "classless/" + themePath
	}

	return h.Link(
		h.ID("pico-theme"),
		h.Rel("stylesheet"),
		h.Href("/_pico/theme/"+themePath),
		h.Attr("data-attr:href", "'/_pico/theme/' + "+th.signalName),
	)
}

func (th *ThemeHandle) SignalDefinition() h.H {
	return h.Div(
		h.Data("signals", fmt.Sprintf("{%s: '%s'}", th.signalName, th.opts.DefaultTheme)),
	)
}

func (th *ThemeHandle) Buttons() []h.H {
	var buttons []h.H

	themes := th.opts.Themes

	for _, theme := range themes {
		themeName := strings.Title(theme)
		buttons = append(buttons,
			h.Button(
				h.Text(themeName),
				h.Data("on:click", fmt.Sprintf("$%s = '%s'", th.signalName, theme)),
				h.Attr("data-theme", theme),
				h.Class("theme-btn"),
			),
		)
	}

	return buttons
}

func (th *ThemeHandle) ColorClassesLink() h.H {
	if !th.opts.ColorClasses {
		return nil
	}
	return h.Link(
		h.ID("pico-color-classes"),
		h.Rel("stylesheet"),
		h.Href("/_pico/color-classes"),
	)
}

func (th *ThemeHandle) HTMLAttr() h.H {
	return h.Attr("data-theme", "$_picoTheme")
}
