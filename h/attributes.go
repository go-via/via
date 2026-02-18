package h

import (
	"fmt"

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

func Role(v string) H {
	return gh.Role(v)
}

// Data attributes automatically have their name prefixed with "data-".
func Data(name, v string) H {
	return gh.Data(name, v)
}

// DataF creates a data attribute with fmt.Sprintf formatting.
func DataF(name, format string, args ...any) H {
	return gh.Data(name, fmt.Sprintf(format, args...))
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
