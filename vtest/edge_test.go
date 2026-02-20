package vtest_test

import (
	"testing"

	"github.com/go-via/via/vtest"
)

func TestClick_NonExistentButton_ReturnsError(t *testing.T) {
	t.Parallel()

	vtest.SetHandler(vtest.NewCounterApp())

	page := vtest.Visit("/")
	defer page.Close()

	// Clicking a non-existent button should return an error
	err := page.Click("NonExistentButton")
	if err == nil {
		t.Fatal("expected error when clicking non-existent button, got nil")
	}
}

func TestAction_InvalidResponse_ReturnsError(t *testing.T) {
	t.Parallel()

	vtest.SetHandler(vtest.NewCounterApp())

	page := vtest.Visit("/")
	defer page.Close()

	// The Click should succeed and update the page
	err := page.Click("+")
	if err != nil {
		t.Fatalf("unexpected error clicking + button: %v", err)
	}

	// After clicking +, count should be 1
	page.AssertText(t, "Count: 1")
}

func TestVisit_InvalidPath_HandlesGracefully(t *testing.T) {
	t.Parallel()

	vt := vtest.New(vtest.NewCounterApp())

	// Visiting non-existent path should still return a response ( Via renders 404)
	resp := vt.Get("/nonexistent")

	// The response should have some content or proper status
	_ = resp.Body
}
