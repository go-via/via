package content

import "github.com/go-via/via/h"

func table(heads []string, rows ...[]h.H) h.H { return captionedTable("", heads, rows...) }

// captionedTable is table with a <caption>; "" leaves it out.
func captionedTable(caption string, heads []string, rows ...[]h.H) h.H {
	head := make([]h.H, 0, len(heads))
	for _, t := range heads {
		head = append(head, h.Th(h.RawAttr("scope", "col"), h.Str(t)))
	}
	// Under 50rem a row stacks and the header row is hidden; with three or
	// more columns a cell needs its head beside it to make sense.
	labelled := len(heads) >= 3
	body := make([]h.H, 0, len(rows))
	for _, r := range rows {
		cells := make([]h.H, 0, len(r))
		for i, c := range r {
			if labelled && i < len(heads) && heads[i] != "" {
				cells = append(cells, h.Td(h.Data("label", heads[i]), c))
				continue
			}
			cells = append(cells, h.Td(c))
		}
		body = append(body, h.Tr(cells...))
	}
	out := []h.H{h.Class("ref-table")}
	if caption != "" {
		out = append(out, h.Caption(h.Str(caption)))
	}
	return h.Table(append(out, h.Thead(h.Tr(head...)), h.Tbody(body...))...)
}

func refTable(head string, rows []row) h.H {
	cells := make([][]h.H, 0, len(rows))
	for _, r := range rows {
		cells = append(cells, []h.H{h.Code(h.Str(r.name)), h.Str(r.use)})
	}
	return table([]string{head, "Does"}, cells...)
}

func helperTable() h.H {
	cells := make([][]h.H, 0, len(dataHelpers))
	for _, d := range dataHelpers {
		cells = append(cells, []h.H{h.Code(h.Str(d.name)), h.Code(h.Str(d.attr)), h.Str(d.use)})
	}
	return table([]string{"Helper", "Attribute", "Does"}, cells...)
}
