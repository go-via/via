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
	v := via.New().Config(via.Options{
		DevMode:       true,
		DocumentTitle: "Search",
		LogLvl:        via.LogLevelWarn,
		// Plugins:       []via.Plugin{LiveReloadPlugin},
	}).AppendToHead(
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
