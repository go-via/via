package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestRenderTable(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	columns := []string{"play", "player", "text"}
	mock.ExpectQuery("SELECT").
		WillReturnRows(
			sqlmock.NewRows(columns).
				AddRow("Hamlet", "Hamlet", "To be or not to be").
				AddRow("Macbeth", "Macbeth", "Out, out brief candle!").
				AddRow("Romeo and Juliet", "Juliet", "O Romeo, Romeo!"),
		)

	rows, err := db.Query("SELECT play, player, text FROM plays")
	assert.NoError(t, err)
	defer rows.Close()

	table, err := RenderTable(rows, []string{"no-wrap", "no-wrap", ""})
	assert.NoError(t, err)
	assert.NotNil(t, table)

	var buf bytes.Buffer
	err = table.Render(&buf)
	assert.NoError(t, err)

	html := buf.String()

	assert.Contains(t, html, "<table>")
	assert.Contains(t, html, "</table>")
	assert.Contains(t, html, "<thead>")
	assert.Contains(t, html, "</thead>")
	assert.Contains(t, html, "<tbody>")
	assert.Contains(t, html, "</tbody>")

	assert.Contains(t, html, `<th scope="col">play</th>`)
	assert.Contains(t, html, `<th scope="col">player</th>`)
	assert.Contains(t, html, `<th scope="col">text</th>`)

	assert.Contains(t, html, `<th scope="row" class="no-wrap">Hamlet</th>`)
	assert.Contains(t, html, `<th scope="row" class="no-wrap">Macbeth</th>`)
	assert.Contains(t, html, `<th scope="row" class="no-wrap">Romeo and Juliet</th>`)

	assert.Contains(t, html, `<td class="no-wrap">Hamlet</td>`)
	assert.Contains(t, html, `<td class="no-wrap">Macbeth</td>`)
	assert.Contains(t, html, `<td class="no-wrap">Juliet</td>`)

	assert.Contains(t, html, "<td>To be or not to be</td>")
	assert.Contains(t, html, "<td>Out, out brief candle!</td>")
	assert.Contains(t, html, "<td>O Romeo, Romeo!</td>")
}

func TestRenderTableWithNilValues(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	columns := []string{"id", "name", "description"}
	mock.ExpectQuery("SELECT").
		WillReturnRows(
			sqlmock.NewRows(columns).
				AddRow(1, "Item 1", nil).
				AddRow(2, nil, "Description 2"),
		)

	rows, err := db.Query("SELECT id, name, description FROM items")
	assert.NoError(t, err)
	defer rows.Close()

	table, err := RenderTable(rows, []string{"no-wrap", "no-wrap", ""})
	assert.NoError(t, err)

	var buf bytes.Buffer
	err = table.Render(&buf)
	assert.NoError(t, err)

	html := buf.String()

	assert.Contains(t, html, `<th scope="row" class="no-wrap">1</th>`)
	assert.Contains(t, html, `<td class="no-wrap">Item 1</td>`)
	assert.Contains(t, html, "<td></td>")

	assert.Contains(t, html, `<th scope="row" class="no-wrap">2</th>`)
}

func TestRenderTableWithByteValues(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	columns := []string{"id", "data"}
	mock.ExpectQuery("SELECT").
		WillReturnRows(
			sqlmock.NewRows(columns).
				AddRow(1, []byte("binary data")),
		)

	rows, err := db.Query("SELECT id, data FROM blobs")
	assert.NoError(t, err)
	defer rows.Close()

	table, err := RenderTable(rows, []string{"no-wrap", "no-wrap"})
	assert.NoError(t, err)

	var buf bytes.Buffer
	err = table.Render(&buf)
	assert.NoError(t, err)

	html := buf.String()
	assert.Contains(t, html, `<td class="no-wrap">binary data</td>`)
}

func TestRenderTableWithSpecialCharacters(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	columns := []string{"id", "content"}
	mock.ExpectQuery("SELECT").
		WillReturnRows(
			sqlmock.NewRows(columns).
				AddRow(1, "<script>alert('XSS')</script>").
				AddRow(2, "A & B"),
		)

	rows, err := db.Query("SELECT id, content FROM texts")
	assert.NoError(t, err)
	defer rows.Close()

	table, err := RenderTable(rows, []string{"", ""})
	assert.NoError(t, err)

	var buf bytes.Buffer
	err = table.Render(&buf)
	assert.NoError(t, err)

	html := buf.String()

	assert.Contains(t, html, "&lt;script&gt;")
	assert.Contains(t, html, "&amp;")

	assert.NotContains(t, html, "<script>alert")
}

func TestRenderTableEmptyRows(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	columns := []string{"id", "name"}
	mock.ExpectQuery("SELECT").
		WillReturnRows(sqlmock.NewRows(columns))

	rows, err := db.Query("SELECT id, name FROM items")
	assert.NoError(t, err)
	defer rows.Close()

	table, err := RenderTable(rows, []string{"", ""})
	assert.NoError(t, err)

	var buf bytes.Buffer
	err = table.Render(&buf)
	assert.NoError(t, err)

	html := buf.String()

	assert.Contains(t, html, "<thead>")
	assert.Contains(t, html, `<th scope="col">id</th>`)
	assert.Contains(t, html, `<th scope="col">name</th>`)

	tbodyStart := strings.Index(html, "<tbody>")
	tbodyEnd := strings.Index(html, "</tbody>")
	assert.True(t, tbodyStart < tbodyEnd)

	tbody := html[tbodyStart+7 : tbodyEnd]
	assert.NotContains(t, tbody, "<tr>")
}

func TestValueToString(t *testing.T) {
	assert.Equal(t, "", valueToString(nil))
	assert.Equal(t, "hello", valueToString([]byte("hello")))
	assert.Equal(t, "42", valueToString(42))
	assert.Equal(t, "3.14", valueToString(3.14))
	assert.Equal(t, "true", valueToString(true))
	assert.Equal(t, "test string", valueToString("test string"))
}
