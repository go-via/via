package content

import "github.com/go-via/via/h"

func table(heads []string, rows ...[]h.H) h.H {
	head := make([]h.H, 0, len(heads))
	for _, t := range heads {
		head = append(head, h.Th(h.Str(t)))
	}
	out := []h.H{h.Class("ref-table"), h.Tr(head...)}
	for _, r := range rows {
		cells := make([]h.H, 0, len(r))
		for _, c := range r {
			cells = append(cells, h.Td(c))
		}
		out = append(out, h.Tr(cells...))
	}
	return h.Table(out...)
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
