package h

import (
	g "maragu.dev/gomponents"
)

func retype(nodes []H) []g.Node {
	if len(nodes) == 0 {
		return nil
	}

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

func Map[T any](items []T, fn func(T) H) H {
	group := g.Map(items, func(t T) g.Node { return fn(t) })
	return H(group)
}

func Concat(slices ...[]H) []H {
	result := make([]H, 0, 64)
	for _, slice := range slices {
		result = append(result, slice...)
	}
	return result
}

func OptionsFromSlice(items []string) []H {
	result := make([]H, len(items))
	for i, item := range items {
		result[i] = Option(Value(item), Text(item))
	}
	return result
}
