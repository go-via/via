package h

import (
	g "maragu.dev/gomponents"
)

func retype(nodes []H) []g.Node {
	list := make([]g.Node, len(nodes))
	for i, node := range nodes {
		if node == nil {
			list[i] = nil
			continue
		}
		list[i] = node.(g.Node)
	}
	return list
}
