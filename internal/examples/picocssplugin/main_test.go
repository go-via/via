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
	page.AssertText(t, "3/12")
}

func TestPicoCSSPluginPageIncrements(t *testing.T) {
	t.Parallel()

	v := picocssplugin.NewPicoCSSPluginPage()
	page := vtest.VisitWith(v.HTTPServeMux(), "/")
	defer page.Close()

	page.AssertText(t, "3/12")

	page.Click("+")
	page.AssertText(t, "4/12")

	page.Click("+")
	page.AssertText(t, "5/12")

	page.Click("+")
	page.AssertText(t, "6/12")
}

func TestPicoCSSPluginPageDecrements(t *testing.T) {
	t.Parallel()

	v := picocssplugin.NewPicoCSSPluginPage()
	page := vtest.VisitWith(v.HTTPServeMux(), "/")
	defer page.Close()

	page.AssertText(t, "3/12")

	page.Click("-")
	page.AssertText(t, "2/12")

	page.Click("-")
	page.AssertText(t, "1/12")

	page.Click("-")
	page.AssertText(t, "0/12")
}
