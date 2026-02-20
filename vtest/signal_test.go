package vtest_test

import (
	"testing"

	"github.com/go-via/via/vtest"
)

func TestCounterWithStepSignal(t *testing.T) {
	t.Parallel()

	vtest.SetHandler(vtest.NewCounterWithStepApp())

	page := vtest.Visit("/")
	defer page.Close()

	page.AssertText(t, "Count: 0")
	page.AssertText(t, "Step: 1")

	// Increment by default step (1)
	if err := page.Click("+"); err != nil {
		t.Fatalf("click + failed: %v", err)
	}
	page.AssertText(t, "Count: 1")

	// Change step to 5
	page.Fill("step", "5")
	page.AssertText(t, "Step: 5")

	// Increment by 5
	if err := page.Click("+"); err != nil {
		t.Fatalf("click + failed: %v", err)
	}
	page.AssertText(t, "Count: 6")

	// Increment by 5 again
	if err := page.Click("+"); err != nil {
		t.Fatalf("click + failed: %v", err)
	}
	page.AssertText(t, "Count: 11")

	// Decrement by 5
	if err := page.Click("-"); err != nil {
		t.Fatalf("click - failed: %v", err)
	}
	page.AssertText(t, "Count: 6")

	// Change step to 10
	page.Fill("step", "10")
	page.AssertText(t, "Step: 10")

	// Decrement by 10
	if err := page.Click("-"); err != nil {
		t.Fatalf("click - failed: %v", err)
	}
	page.AssertText(t, "Count: -4")
}
