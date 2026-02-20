package vtest_test

import (
	"testing"

	"github.com/go-via/via/vtest"
)

func TestPageCounter(t *testing.T) {
	t.Parallel()

	vtest.SetHandler(vtest.NewCounterApp())

	page := vtest.Visit("/")
	defer page.Close()

	page.AssertText(t, "Count: 0")

	if err := page.Click("+"); err != nil {
		t.Fatalf("click + failed: %v", err)
	}
	page.AssertText(t, "Count: 1")

	if err := page.Click("+"); err != nil {
		t.Fatalf("click + failed: %v", err)
	}
	page.AssertText(t, "Count: 2")

	if err := page.Click("-"); err != nil {
		t.Fatalf("click - failed: %v", err)
	}
	page.AssertText(t, "Count: 1")

	if err := page.Click("-"); err != nil {
		t.Fatalf("click - failed: %v", err)
	}
	page.AssertText(t, "Count: 0")
}

func TestPageTodoList(t *testing.T) {
	t.Parallel()

	vtest.SetHandler(vtest.NewTodoApp())

	page := vtest.Visit("/")
	defer page.Close()

	page.AssertText(t, "Todo List")
	page.AssertText(t, "Items: 0")

	if err := page.Click("Add"); err != nil {
		t.Fatalf("click Add failed: %v", err)
	}
	page.AssertText(t, "Items: 1")
	page.AssertText(t, "New todo")

	if err := page.Click("Add"); err != nil {
		t.Fatalf("click Add failed: %v", err)
	}
	page.AssertText(t, "Items: 2")

	if err := page.Click("Clear"); err != nil {
		t.Fatalf("click Clear failed: %v", err)
	}
	page.AssertText(t, "Items: 0")
}

func TestPageGreeter(t *testing.T) {
	t.Parallel()

	vtest.SetHandler(vtest.NewGreeterApp())

	page := vtest.Visit("/")
	defer page.Close()

	page.AssertText(t, "Hello, World!")

	if err := page.Click("Greet"); err != nil {
		t.Fatalf("click Greet failed: %v", err)
	}
	page.AssertText(t, "Hello, Alice!")

	if err := page.Click("Reset"); err != nil {
		t.Fatalf("click Reset failed: %v", err)
	}
	page.AssertText(t, "Hello, World!")
}
