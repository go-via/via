package picocss

import (
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
)

func TestAllThemes_ContainsExpected(t *testing.T) {
	expected := []ThemeName{ThemeAmber, ThemeBlue, ThemeCyan, ThemeFuchsia, ThemeGreen, ThemeGrey, ThemeIndigo, ThemeJade, ThemeLime, ThemeOrange, ThemePink, ThemePumpkin, ThemePurple, ThemeRed, ThemeSand, ThemeSlate, ThemeViolet, ThemeYellow, ThemeZinc}

	if len(AllThemes) != len(expected) {
		t.Errorf("expected %d themes, got %d", len(expected), len(AllThemes))
	}

	for i, theme := range expected {
		if i >= len(AllThemes) {
			break
		}
		if AllThemes[i] != theme {
			t.Errorf("expected theme %s at index %d, got %s", theme, i, AllThemes[i])
		}
	}
}

func TestOptions_Defaults(t *testing.T) {
	opts := Options{}
	p := New(opts)

	if p.opts.DefaultTheme != ThemeBlue {
		t.Errorf("expected default theme blue, got %s", p.opts.DefaultTheme)
	}

	if len(p.themes) != len(AllThemes) {
		t.Errorf("expected all themes by default, got %d", len(p.themes))
	}
}

func TestOptions_CustomTheme(t *testing.T) {
	opts := Options{
		Themes:       []ThemeName{ThemePurple, ThemeAmber},
		DefaultTheme: ThemePurple,
	}
	p := New(opts)

	if p.opts.DefaultTheme != ThemePurple {
		t.Errorf("expected default theme purple, got %s", p.opts.DefaultTheme)
	}

	if len(p.themes) != 2 {
		t.Errorf("expected 2 themes, got %d", len(p.themes))
	}
}

func TestOptions_InvalidDefaultTheme(t *testing.T) {
	opts := Options{
		Themes:       []ThemeName{ThemeBlue, ThemePurple},
		DefaultTheme: "invalid-theme",
	}
	p := New(opts)

	if p.opts.DefaultTheme != ThemeBlue {
		t.Errorf("expected default to fall back to first theme, got %s", p.opts.DefaultTheme)
	}
}

func TestOptions_Classless(t *testing.T) {
	opts := Options{
		Themes:       []ThemeName{ThemeBlue},
		DefaultTheme: ThemeBlue,
		Classless:    true,
	}
	p := New(opts)

	if !p.opts.Classless {
		t.Error("expected Classless to be true")
	}
}

func TestOptions_ColorClasses(t *testing.T) {
	opts := Options{
		Themes:       []ThemeName{ThemeBlue},
		DefaultTheme: ThemeBlue,
		ColorClasses: true,
	}
	p := New(opts)

	if !p.opts.ColorClasses {
		t.Error("expected ColorClasses to be true")
	}
}

func TestPlugin_HasHeadLinkField(t *testing.T) {
	p := New(Options{Themes: []ThemeName{ThemeBlue}})

	if p.HeadLink == nil {
		t.Error("expected HeadLink to be set after plugin creation")
	}
}

func TestTheme_DefaultOptions(t *testing.T) {
	c := &via.Composition{}
	th := Theme(c, Options{})

	if th.opts.DefaultTheme != ThemeBlue {
		t.Errorf("expected default theme blue, got %s", th.opts.DefaultTheme)
	}

	if len(th.opts.Themes) != len(AllThemes) {
		t.Errorf("expected all themes, got %d", len(th.opts.Themes))
	}
}

func TestTheme_CustomOptions(t *testing.T) {
	c := &via.Composition{}
	th := Theme(c, Options{
		Themes:       []ThemeName{ThemeRed, ThemeGreen},
		DefaultTheme: ThemeRed,
	})

	if th.opts.DefaultTheme != ThemeRed {
		t.Errorf("expected red, got %s", th.opts.DefaultTheme)
	}

	if len(th.opts.Themes) != 2 {
		t.Errorf("expected 2 themes, got %d", len(th.opts.Themes))
	}
}

func TestThemeHandle_Link(t *testing.T) {
	c := &via.Composition{}
	th := Theme(c, Options{DefaultTheme: ThemeBlue})
	link := th.Link()

	if link == nil {
		t.Error("expected Link to return non-nil")
	}
}

func TestThemeHandle_SignalDefinition(t *testing.T) {
	c := &via.Composition{}
	th := Theme(c, Options{DefaultTheme: ThemeBlue})
	sigDef := th.SignalDefinition()

	if sigDef == nil {
		t.Error("expected SignalDefinition to return non-nil")
	}
}

func TestThemeHandle_HTMLAttr(t *testing.T) {
	c := &via.Composition{}
	th := Theme(c, Options{DefaultTheme: ThemeBlue})
	htmlAttr := th.HTMLAttr()

	if htmlAttr == nil {
		t.Error("expected HTMLAttr to return non-nil")
	}
}

func TestThemeHandle_ColorClassesLink_WhenEnabled(t *testing.T) {
	c := &via.Composition{}
	th := Theme(c, Options{ColorClasses: true})
	link := th.ColorClassesLink()

	if link == nil {
		t.Error("expected ColorClassesLink to return non-nil when ColorClasses is true")
	}
}

func TestThemeHandle_ColorClassesLink_WhenDisabled(t *testing.T) {
	c := &via.Composition{}
	th := Theme(c, Options{ColorClasses: false})
	link := th.ColorClassesLink()

	if link != nil {
		t.Error("expected ColorClassesLink to return nil when ColorClasses is false")
	}
}

func TestPlugin_ColorClassesLink_NotSet(t *testing.T) {
	p := New(Options{ColorClasses: false, Themes: []ThemeName{ThemeBlue}})
	link := p.ColorClassesLink()

	if link != nil {
		t.Error("expected ColorClassesLink to return nil when ColorClasses is false")
	}
}

func TestPlugin_ColorClassesLink_EmptyBeforeFetch(t *testing.T) {
	p := New(Options{ColorClasses: true, Themes: []ThemeName{ThemeBlue}})
	link := p.ColorClassesLink()

	if link != nil {
		t.Error("expected ColorClassesLink to return nil before FetchThemes is called")
	}
}

func TestPlugin_ImplementsViaPlugin(t *testing.T) {
	p := New(Options{Themes: []ThemeName{ThemeBlue}})

	var _ via.Plugin = p

	assert.NotNil(t, p.Register)
}
