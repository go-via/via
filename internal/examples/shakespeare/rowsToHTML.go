package main

import (
	"database/sql"
	"fmt"

	"github.com/go-via/via/h"
)

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
