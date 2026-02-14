package vtest

import (
	"net/http"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

// NewCounterApp creates a minimal counter app for testing.
func NewCounterApp() http.Handler {
	v := via.New()

	v.Page("/", func(c *via.Composition) {
		count := via.State(c, 0)

		increment := via.Action(c, func(s *via.Session) {
			count.Set(s, count.Get(s)+1)
		})

		decrement := via.Action(c, func(s *via.Session) {
			count.Set(s, count.Get(s)-1)
		})

		c.View(func(s *via.Session) h.H {
			return h.Div(
				h.H1(h.Text("Counter")),
				h.P(h.Textf("Count: %d", count.Get(s))),
				h.Button(h.Text("-"), decrement.OnClick()),
				h.Button(h.Text("+"), increment.OnClick()),
			)
		})
	})

	return v.HTTPServeMux()
}

// NewCounterWithStepApp creates a counter with step signal for testing.
// Step is a client-side signal (browser state) that controls increment amount.
func NewCounterWithStepApp() http.Handler {
	v := via.New()

	v.Page("/", func(c *via.Composition) {
		count := via.State(c, 0)
		step := via.Signal(c, 1)

		increment := via.Action(c, func(s *via.Session) {
			count.Set(s, count.Get(s)+step.Get(s))
		})

		decrement := via.Action(c, func(s *via.Session) {
			count.Set(s, count.Get(s)-step.Get(s))
		})

		c.View(func(s *via.Session) h.H {
			return h.Div(
				h.H1(h.Text("Counter with Step")),
				h.P(h.Textf("Count: %d", count.Get(s))),
				h.P(h.Text("Step: "), h.Span(step.Text())),
				h.Input(h.Type("number"), h.Name("step"), step.Bind()),
				h.Button(h.Text("-"), decrement.OnClick()),
				h.Button(h.Text("+"), increment.OnClick()),
			)
		})
	})

	return v.HTTPServeMux()
}

// NewTodoApp creates a minimal todo app for testing.
func NewTodoApp() http.Handler {
	v := via.New()

	v.Page("/", func(c *via.Composition) {
		todos := via.State(c, []string{})

		addTodo := via.Action(c, func(s *via.Session) {
			current := todos.Get(s)
			todos.Set(s, append(current, "New todo"))
		})

		clearAll := via.Action(c, func(s *via.Session) {
			todos.Set(s, []string{})
		})

		c.View(func(s *via.Session) h.H {
			items := todos.Get(s)

			listItems := []h.H{}
			for _, todo := range items {
				listItems = append(listItems, h.Li(h.Text(todo)))
			}

			return h.Div(
				h.H1(h.Text("Todo List")),
				h.P(h.Textf("Items: %d", len(items))),
				h.Ul(listItems...),
				h.Button(h.Text("Add"), addTodo.OnClick()),
				h.Button(h.Text("Clear"), clearAll.OnClick()),
			)
		})
	})

	return v.HTTPServeMux()
}

// NewGreeterApp creates a minimal greeter app for testing.
func NewGreeterApp() http.Handler {
	v := via.New()

	v.Page("/", func(c *via.Composition) {
		name := via.State(c, "World")

		greet := via.Action(c, func(s *via.Session) {
			name.Set(s, "Alice")
		})

		reset := via.Action(c, func(s *via.Session) {
			name.Set(s, "World")
		})

		c.View(func(s *via.Session) h.H {
			return h.Div(
				h.H1(h.Text("Greeter")),
				h.P(h.Textf("Hello, %s!", name.Get(s))),
				h.Button(h.Text("Greet"), greet.OnClick()),
				h.Button(h.Text("Reset"), reset.OnClick()),
			)
		})
	})

	return v.HTTPServeMux()
}

// NewComponentCounterApp creates a counter app using components for testing.
func NewComponentCounterApp() http.Handler {
	v := via.New()

	v.Page("/", func(c *via.Composition) {
		makeCounter := func(label string) via.ComposeFn {
			return func(c *via.Composition) {
				count := via.State(c, 0)
				increment := via.Action(c, func(s *via.Session) {
					count.Set(s, count.Get(s)+1)
				})
				c.View(func(s *via.Session) h.H {
					return h.Div(
						h.P(h.Textf("%s: %d", label, count.Get(s))),
						h.Button(h.Text("+"), increment.OnClick()),
					)
				})
			}
		}

		counter := c.Component(makeCounter("Count"))

		c.View(func(s *via.Session) h.H {
			return h.Div(
				h.H1(h.Text("Component Counter")),
				counter.Mount(s),
			)
		})
	})

	return v.HTTPServeMux()
}

// NewNestedComponentApp creates a nested component app for testing.
func NewNestedComponentApp() http.Handler {
	v := via.New()

	v.Page("/", func(c *via.Composition) {
		makeCounter := func(label string) via.ComposeFn {
			return func(c *via.Composition) {
				count := via.State(c, 0)
				increment := via.Action(c, func(s *via.Session) {
					count.Set(s, count.Get(s)+1)
				})
				c.View(func(s *via.Session) h.H {
					return h.Div(
						h.P(h.Textf("%s: %d", label, count.Get(s))),
						h.Button(h.Text("+"), increment.OnClick()),
					)
				})
			}
		}

		makePanel := func() via.ComposeFn {
			return func(c *via.Composition) {
				counterA := c.Component(makeCounter("Counter A"))
				counterB := c.Component(makeCounter("Counter B"))
				c.View(func(s *via.Session) h.H {
					return h.Div(
						h.H2(h.Text("Panel")),
						counterA.Mount(s),
						counterB.Mount(s),
					)
				})
			}
		}

		panel := c.Component(makePanel())

		c.View(func(s *via.Session) h.H {
			return h.Div(
				h.H1(h.Text("Nested Components")),
				panel.Mount(s),
			)
		})
	})

	return v.HTTPServeMux()
}
