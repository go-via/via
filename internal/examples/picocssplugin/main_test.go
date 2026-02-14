package main_test

import (
	"testing"

	picocssplugin "github.com/go-via/via/internal/examples/picocssplugin"
	"github.com/go-via/via/vtest"
)

func TestPicoCSSPluginPage(t *testing.T) {
	t.Parallel()

	v := picocssplugin.NewPicoCSSPluginPage()
	page := vtest.VisitWith(v.HTTPServeMux(), "/")
	defer page.Close()

	page.AssertText(t, "Build Reactive Web Apps with Go")
	page.AssertText(t, "Zero JavaScript")
	page.AssertText(t, "Features: 0")
}

func TestPicoCSSPluginPageIncrements(t *testing.T) {
	t.Parallel()

	v := picocssplugin.NewPicoCSSPluginPage()
	page := vtest.VisitWith(v.HTTPServeMux(), "/")
	defer page.Close()

	page.AssertText(t, "Features: 0")

	page.Click("+")
	page.AssertText(t, "Features: 1")

	page.Click("+")
	page.AssertText(t, "Features: 2")
}

func TestPicoCSSPluginPageDecrements(t *testing.T) {
	t.Parallel()

	v := picocssplugin.NewPicoCSSPluginPage()
	page := vtest.VisitWith(v.HTTPServeMux(), "/")
	defer page.Close()

	page.Click("+")
	page.Click("+")
	page.AssertText(t, "Features: 2")

	page.Click("-")
	page.AssertText(t, "Features: 1")

	page.Click("-")
	page.AssertText(t, "Features: 0")

	page.Click("-")
	page.AssertText(t, "Features: 0")
}

