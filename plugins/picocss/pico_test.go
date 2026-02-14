package picocss

import (
	"testing"
)

func TestAllThemes_ContainsExpected(t *testing.T) {
	expected := []string{"amber", "blue", "cyan", "fuchsia", "green", "grey", "indigo", "jade", "lime", "orange", "pink", "pumpkin", "purple", "red", "sand", "slate", "violet", "yellow", "zinc"}

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

	if p.opts.DefaultTheme != "blue" {
		t.Errorf("expected default theme blue, got %s", p.opts.DefaultTheme)
	}

	if len(p.themes) != len(AllThemes) {
		t.Errorf("expected all themes by default, got %d", len(p.themes))
	}
}

func TestOptions_CustomTheme(t *testing.T) {
	opts := Options{
		Themes:       []string{"purple", "amber"},
		DefaultTheme: "purple",
	}
	p := New(opts)

	if p.opts.DefaultTheme != "purple" {
		t.Errorf("expected default theme purple, got %s", p.opts.DefaultTheme)
	}

	if len(p.themes) != 2 {
		t.Errorf("expected 2 themes, got %d", len(p.themes))
	}
}

func TestOptions_InvalidDefaultTheme(t *testing.T) {
	opts := Options{
		Themes:       []string{"blue", "purple"},
		DefaultTheme: "invalid-theme",
	}
	p := New(opts)

	if p.opts.DefaultTheme != "blue" {
		t.Errorf("expected default to fall back to first theme, got %s", p.opts.DefaultTheme)
	}
}

func TestOptions_Classless(t *testing.T) {
	opts := Options{
		Themes:       []string{"blue"},
		DefaultTheme: "blue",
		Classless:    true,
	}
	p := New(opts)

	if !p.opts.Classless {
		t.Error("expected Classless to be true")
	}
}

func TestOptions_ColorClasses(t *testing.T) {
	opts := Options{
		Themes:       []string{"blue"},
		DefaultTheme: "blue",
		ColorClasses: true,
	}
	p := New(opts)

	if !p.opts.ColorClasses {
		t.Error("expected ColorClasses to be true")
	}
}

func TestPlugin_HasHeadLinkField(t *testing.T) {
	p := New(Options{Themes: []string{"blue"}})

	if p.HeadLink == nil {
		t.Error("expected HeadLink to be set after plugin creation")
	}
}
