package main_test

import (
	"regexp"
	"testing"

	counter "github.com/go-via/via/internal/examples/counter"
	"github.com/go-via/via/vtest"
)

func TestCounter(t *testing.T) {
	t.Parallel()

	v := counter.NewCounterPage()
	page := vtest.VisitWith(v.HTTPServeMux(), "/")
	defer page.Close()

	page.AssertText(t, "Counter Example")
	page.AssertText(t, "Count: 0")

	page.Click("+")
	page.AssertText(t, "Count: 1")

	page.Click("+")
	page.AssertText(t, "Count: 2")

	page.Click("-")
	page.AssertText(t, "Count: 1")

	page.Click("-")
	page.AssertText(t, "Count: 0")
}

func TestCounterWithStep(t *testing.T) {
	t.Parallel()

	v := counter.NewCounterPage()
	page := vtest.VisitWith(v.HTTPServeMux(), "/")
	defer page.Close()

	page.AssertText(t, "Count: 0")

	page.Fill("step", "5")
	page.Click("+")
	page.AssertText(t, "Count: 5")

	page.Click("+")
	page.AssertText(t, "Count: 10")

	page.Fill("step", "3")
	page.Click("-")
	page.AssertText(t, "Count: 7")
}

func TestCounterHTMLOutput(t *testing.T) {
	v := counter.NewCounterPage()
	tester := vtest.New(v.HTTPServeMux())

	resp := tester.Get("/")
	resp.AssertStatus(t, 200)

	html := resp.Body.String()

	// Verify step input has data-bind attribute (without $ prefix)
	if !containsPattern(html, `<input[^>]+type="number"[^>]+data-bind="[a-f0-9]{32}"`) {
		t.Errorf("Step input missing data-bind attribute with signal ID")
	}

	t.Logf("HTML output:\n%s", html)
}

func containsPattern(s, pattern string) bool {
	matched, _ := regexp.MatchString(pattern, s)
	return matched
}
