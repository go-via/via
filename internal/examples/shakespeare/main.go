package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	_ "github.com/mattn/go-sqlite3"
)

type DataSource interface {
	Open()
	Query(str string) (*sql.Rows, error)
	Close() error
}

type ShakeDB struct {
	db             *sql.DB
	findByTextStmt *sql.Stmt
}

func (shakeDB *ShakeDB) Prepare() {
	db, err := sql.Open("sqlite3", "shake.db")
	if err != nil {
		log.Fatal(err)
	}
	stmt, err := db.Prepare(`select play,player,plays.text 
	from playsearch inner join plays on playsearch.playsrowid=plays.rowid where playsearch.text match ?
	order by plays.play, plays.player limit 200;`)
	if err != nil {
		log.Fatal(err)
	}
	shakeDB.db = db
	shakeDB.findByTextStmt = stmt
}

func (shakeDB *ShakeDB) Query(str string) (*sql.Rows, error) {
	return shakeDB.findByTextStmt.Query(str)
}

func (shakeDB *ShakeDB) Close() {
	if shakeDB.db != nil {
		shakeDB.db.Close()
		shakeDB.db = nil
	}
}

func main() {
	v := via.New()

	v.Config(via.Options{
		DevMode:       true,
		DocumentTitle: "Search",
		LogLvl:        via.LogLevelWarn,
	})

	v.AppendToHead(
		h.Link(h.Rel("stylesheet"), h.Href("https://cdn.jsdelivr.net/npm/@picocss/pico@2/css/pico.min.css")),
		h.StyleEl(h.Raw(".no-wrap { white-space: nowrap; }")),
	)
	shakeDB := &ShakeDB{}
	shakeDB.Prepare()
	defer shakeDB.Close()

	v.Page("/", func(c *via.Context) {
		query := c.Signal("whether tis")
		var rowsTable H
		runQuery := func() {
			qry := query.String()
			start := time.Now()
			rows, error := shakeDB.Query(qry)
			fmt.Println("query ", qry, "took", time.Since(start))
			if error != nil {
				rowsTable = h.Div(h.Text("Error: " + error.Error()))
			} else {
				table, err := RenderTable(rows, []string{"no-wrap", "no-wrap", ""})
				if err != nil {
					rowsTable = h.Div(h.Text("Error: " + err.Error()))
				} else {
					rowsTable = table
				}
			}
		}
		runQueryAction := c.Action(func() {
			runQuery()
			c.Sync()
		})
		runQuery()
		c.View(func() h.H {
			return h.Div(
				h.H2(h.Text("Search")), h.FieldSet(
					h.Attr("role", "group"),
					h.Input(
						h.Type("text"),
						query.Bind(),
						h.Attr("autofocus"),
						runQueryAction.OnKeyDown("Enter"),
					),
					h.Button(h.Text("Search"), runQueryAction.OnClick())),
				rowsTable,
			)
		})
	})

	v.Start()
}

type H = h.H

func valueToString(v any) string {
	if v == nil {
		return ""
	}
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return fmt.Sprint(v)
}

// RenderTable takes sql.Rows and an array of CSS class names for each column.
// Returns a complete HTML table as a gomponent.
func RenderTable(rows *sql.Rows, columnClasses []string) (H, error) {
	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	headerCells := make([]h.H, len(cols))
	for i, col := range cols {
		headerCells[i] = h.Th(h.Attr("scope", "col"), h.Text(col))
	}
	thead := h.THead(h.Tr(headerCells...))

	var bodyRows []h.H
	for rows.Next() {
		values := make([]any, len(cols))
		scanArgs := make([]any, len(cols))
		for i := range values {
			scanArgs[i] = &values[i]
		}

		if err := rows.Scan(scanArgs...); err != nil {
			return nil, err
		}

		cells := make([]h.H, len(values))
		if len(values) > 0 {
			var thAttrs []h.H
			thAttrs = append(thAttrs, h.Attr("scope", "row"))
			if len(columnClasses) > 0 && columnClasses[0] != "" {
				thAttrs = append(thAttrs, h.Class(columnClasses[0]))
			}
			thAttrs = append(thAttrs, h.Text(valueToString(values[0])))
			cells[0] = h.Th(thAttrs...)

			for i := 1; i < len(values); i++ {
				var tdAttrs []h.H
				if i < len(columnClasses) && columnClasses[i] != "" {
					tdAttrs = append(tdAttrs, h.Class(columnClasses[i]))
				}
				tdAttrs = append(tdAttrs, h.Text(valueToString(values[i])))
				cells[i] = h.Td(tdAttrs...)
			}
		}

		bodyRows = append(bodyRows, h.Tr(cells...))
	}

	if err := rows.Err(); err != nil {
		return nil, err
	}

	tbody := h.TBody(bodyRows...)
	return h.Table(thead, tbody), nil
}
