package vtest_test

import (
	"testing"

	"github.com/go-via/via/vtest"
)

func TestComponentCounter(t *testing.T) {
	t.Parallel()

	page := vtest.VisitWith(vtest.NewComponentCounterApp(), "/")
	defer page.Close()

	page.AssertText(t, "Component Counter")
	page.AssertText(t, "Count")
	page.AssertText(t, "Count: 0")

	page.Click("+")
	page.AssertText(t, "Count: 1")

	page.Click("+")
	page.AssertText(t, "Count: 2")
}

func TestNestedComponents(t *testing.T) {
	t.Parallel()

	page := vtest.VisitWith(vtest.NewNestedComponentApp(), "/")
	defer page.Close()

	page.AssertText(t, "Nested Components")
	page.AssertText(t, "Panel")

	// Initial state
	page.AssertText(t, "Counter A: 0")

	// Click should increment first counter
	page.Click("+")
	page.AssertText(t, "Counter A: 1")
}
