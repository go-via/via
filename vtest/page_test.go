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

	page.Click("+")
	page.AssertText(t, "Count: 1")

	page.Click("+")
	page.AssertText(t, "Count: 2")

	page.Click("-")
	page.AssertText(t, "Count: 1")

	page.Click("-")
	page.AssertText(t, "Count: 0")
}

func TestPageTodoList(t *testing.T) {
	t.Parallel()

	vtest.SetHandler(vtest.NewTodoApp())

	page := vtest.Visit("/")
	defer page.Close()

	page.AssertText(t, "Todo List")
	page.AssertText(t, "Items: 0")

	page.Click("Add")
	page.AssertText(t, "Items: 1")
	page.AssertText(t, "New todo")

	page.Click("Add")
	page.AssertText(t, "Items: 2")

	page.Click("Clear")
	page.AssertText(t, "Items: 0")
}

func TestPageGreeter(t *testing.T) {
	t.Parallel()

	vtest.SetHandler(vtest.NewGreeterApp())

	page := vtest.Visit("/")
	defer page.Close()

	page.AssertText(t, "Hello, World!")

	page.Click("Greet")
	page.AssertText(t, "Hello, Alice!")

	page.Click("Reset")
	page.AssertText(t, "Hello, World!")
}
