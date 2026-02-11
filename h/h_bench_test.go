package h

import (
	"bytes"
	"testing"
)

func BenchmarkRetype(b *testing.B) {
	nodes := []H{
		Text("test1"),
		Text("test2"),
		Text("test3"),
		Text("test4"),
		Text("test5"),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = retype(nodes)
	}
}

func BenchmarkSimpleDiv(b *testing.B) {
	var buf bytes.Buffer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		node := Div(Text("Hello"))
		_ = node.Render(&buf)
	}
}

func BenchmarkNestedElements(b *testing.B) {
	var buf bytes.Buffer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		node := Div(
			H1(Text("Title")),
			P(Text("Paragraph")),
			Div(
				Span(Text("Nested")),
				Span(Text("Content")),
			),
		)
		_ = node.Render(&buf)
	}
}

func BenchmarkLargeDocument(b *testing.B) {
	var buf bytes.Buffer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		items := make([]H, 100)
		for j := 0; j < 100; j++ {
			items[j] = Li(Textf("Item %d", j))
		}
		node := Div(
			H1(Text("Large List")),
			Ul(items...),
		)
		_ = node.Render(&buf)
	}
}

func BenchmarkAttributes(b *testing.B) {
	var buf bytes.Buffer
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		node := Div(
			ID("test"),
			Class("container active"),
			Data("value", "123"),
			Style("color: red"),
			Text("Content"),
		)
		_ = node.Render(&buf)
	}
}

func BenchmarkRetypeWithNils(b *testing.B) {
	nodes := []H{
		Text("test1"),
		nil,
		Text("test2"),
		nil,
		Text("test3"),
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = retype(nodes)
	}
}

// Simulate actual Via usage: view function called on every Sync()
func BenchmarkRealisticViewRender(b *testing.B) {
	var buf bytes.Buffer
	count := 0

	// Simulate a typical Via view function
	viewFn := func() H {
		return Div(
			H1(Text("Counter Example")),
			P(Textf("Count: %d", count)),
			Div(
				Button(Text("-")),
				Button(Text("+")),
			),
		)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		count++
		node := viewFn()
		_ = node.Render(&buf)
	}
}

// Benchmark realistic view with more elements (like a real app)
func BenchmarkRealisticComplexView(b *testing.B) {
	var buf bytes.Buffer
	items := []string{"Item 1", "Item 2", "Item 3", "Item 4", "Item 5"}

	viewFn := func() H {
		listItems := make([]H, len(items))
		for i, item := range items {
			listItems[i] = Li(
				Div(
					Span(Text(item)),
					Button(Text("Edit")),
					Button(Text("Delete")),
				),
			)
		}

		return Div(
			H1(Text("Todo List")),
			Ul(listItems...),
			Div(
				Input(Type("text"), Placeholder("New item")),
				Button(Text("Add")),
			),
		)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		node := viewFn()
		_ = node.Render(&buf)
	}
}
