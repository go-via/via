package h

import (
	"slices"
	"strings"

	gh "maragu.dev/gomponents/html"
)

func Href(v string) H {
	return gh.Href(v)
}

func Type(v string) H {
	return gh.Type(v)
}

func Src(v string) H {
	return gh.Src(v)
}

func ID(v string) H {
	return gh.ID(v)
}

func Value(v string) H {
	return gh.Value(v)
}

func Name(v string) H {
	return gh.Name(v)
}

func Placeholder(v string) H {
	return gh.Placeholder(v)
}

func Rel(v string) H {
	return gh.Rel(v)
}

func Class(v string) H {
	return gh.Class(v)
}

// Classes joins many class names with spaces and renders one class
// attribute. Empty entries are skipped so callers can branch with
// inline conditionals without producing a ragged class list:
//
//	h.Classes("btn", h.IfStr(active, "btn-primary"), "lg")
func Classes(parts ...string) H {
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p != "" {
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return gh.Class(strings.Join(out, " "))
}

// ClassMap renders a class attribute that includes each key whose value
// is true. Keys are emitted in sorted order so the output is stable
// across renders — handy for snapshot tests and browser caching.
func ClassMap(m map[string]bool) H {
	if len(m) == 0 {
		return nil
	}
	keys := make([]string, 0, len(m))
	for k, v := range m {
		if !v || k == "" {
			continue
		}
		keys = append(keys, k)
	}
	if len(keys) == 0 {
		return nil
	}
	slices.Sort(keys)
	return gh.Class(strings.Join(keys, " "))
}

// IfStr returns s if cond is true, "" otherwise. Pairs with Classes /
// Style for inline conditional fragments.
func IfStr(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}

func Role(v string) H {
	return gh.Role(v)
}

func Min(v string) H {
	return gh.Min(v)
}

func Max(v string) H {
	return gh.Max(v)
}

func Step(v string) H {
	return gh.Step(v)
}

// Data attributes automatically have their name prefixed with "data-".
func Data(name, v string) H {
	return gh.Data(name, v)
}

// DataF creates a data attribute with fmt.Sprintf formatting.
func DataF(name, format string, args ...any) H {
	return gh.Data(name, expr(format, args))
}

func For(v string) H {
	return gh.For(v)
}

func Selected() H {
	return gh.Selected()
}

func Aria(name, v string) H {
	return gh.Aria(name, v)
}

func AriaLabel(v string) H {
	return gh.Aria("label", v)
}

func AriaHidden() H {
	return gh.Aria("hidden", "true")
}

func AriaExpanded(v string) H {
	return gh.Aria("expanded", v)
}

func AriaDisabled() H {
	return gh.Aria("disabled", "true")
}

func AriaChecked() H {
	return gh.Aria("checked", "true")
}

func AriaControls(v string) H {
	return gh.Aria("controls", v)
}

func AriaDescribedBy(v string) H {
	return gh.Aria("describedby", v)
}
