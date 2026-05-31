package h

import (
	"io"
	"slices"
)

// attrNode is a name=value attribute carrying its pre-escaped value as
// a string. The pointer receiver lets [H] storage skip the heap-box
// step that a slice or value-receiver type would trigger.
type attrNode struct {
	name  string
	value string // already HTML-escaped
}

func (a *attrNode) Render(w io.Writer) error {
	sw, _ := w.(stringWriter)
	if err := writeString(w, sw, " "); err != nil {
		return err
	}
	if err := writeString(w, sw, a.name); err != nil {
		return err
	}
	if err := writeString(w, sw, `="`); err != nil {
		return err
	}
	if err := writeString(w, sw, a.value); err != nil {
		return err
	}
	return writeString(w, sw, `"`)
}

func (*attrNode) isAttr() {}

// boolAttrNode is a name-only (boolean) attribute, e.g. `required`.
type boolAttrNode struct{ name string }

func (a *boolAttrNode) Render(w io.Writer) error {
	sw, _ := w.(stringWriter)
	if err := writeString(w, sw, " "); err != nil {
		return err
	}
	return writeString(w, sw, a.name)
}

func (*boolAttrNode) isAttr() {}

// buildAttr stores name and pre-escaped value on the heap as a single
// attribute node.
func buildAttr(name, value string) H {
	// htmlEscape returns the input string when no character needed
	// replacement — that path costs zero allocation.
	return &attrNode{name: name, value: htmlEscape(value)}
}

func buildBool(name string) H {
	return &boolAttrNode{name: name}
}

// Attr creates an attribute. A single value produces name="escaped";
// no value produces a boolean attribute (`required`); more than one
// value panics.
func Attr(name string, value ...string) H {
	switch len(value) {
	case 0:
		return buildBool(name)
	case 1:
		return buildAttr(name, value[0])
	default:
		panic("h: attribute must be name or name+value")
	}
}

// dataAttrNode emits a `data-<suffix>="value"` attribute without
// materialising the concatenated name string at construction.
type dataAttrNode struct {
	suffix string
	value  string // already HTML-escaped
}

func (a *dataAttrNode) Render(w io.Writer) error {
	sw, _ := w.(stringWriter)
	if err := writeString(w, sw, " data-"); err != nil {
		return err
	}
	if err := writeString(w, sw, a.suffix); err != nil {
		return err
	}
	if err := writeString(w, sw, `="`); err != nil {
		return err
	}
	if err := writeString(w, sw, a.value); err != nil {
		return err
	}
	return writeString(w, sw, `"`)
}

func (*dataAttrNode) isAttr() {}

// Data is shorthand for `data-<name>="value"`. Specialised over
// [Attr]("data-"+name, value) to skip the per-call name-prefix
// concatenation.
func Data(name, value string) H {
	return &dataAttrNode{suffix: name, value: htmlEscape(value)}
}

// One shorthand per common HTML attribute — each emits `name="value"`
// (HTML-escaped) via [buildAttr]. For an attribute without a shorthand use
// [Attr]; for data-* use [Data]; for boolean attributes see [Selected],
// [Checked], [Required], [Disabled].
func Href(v string) H        { return buildAttr("href", v) }
func Type(v string) H        { return buildAttr("type", v) }
func Src(v string) H         { return buildAttr("src", v) }
func ID(v string) H          { return buildAttr("id", v) }
func Value(v string) H       { return buildAttr("value", v) }
func Name(v string) H        { return buildAttr("name", v) }
func Placeholder(v string) H { return buildAttr("placeholder", v) }
func Rel(v string) H         { return buildAttr("rel", v) }
func Role(v string) H        { return buildAttr("role", v) }
func Min(v string) H         { return buildAttr("min", v) }
func Max(v string) H         { return buildAttr("max", v) }
func Step(v string) H        { return buildAttr("step", v) }
func For(v string) H         { return buildAttr("for", v) }
func Lang(v string) H        { return buildAttr("lang", v) }
func Content(v string) H     { return buildAttr("content", v) }
func Charset(v string) H     { return buildAttr("charset", v) }

// Style emits an inline `style="..."` attribute. For the
// `<style>...</style>` element use [StyleEl].
func Style(v string) H { return buildAttr("style", v) }

// Styles joins non-empty CSS declarations with `;` and emits one
// inline style attribute. Skip-on-empty makes inline conditionals
// natural:
//
//	h.Styles("flex:1", h.IfStr(done, "text-decoration:line-through"))
func Styles(parts ...string) H {
	total := 0
	count := 0
	for _, p := range parts {
		if p == "" {
			continue
		}
		if count > 0 {
			total++ // separator
		}
		total += len(p)
		count++
	}
	if count == 0 {
		return nil
	}
	out := make([]byte, 0, total)
	first := true
	for _, p := range parts {
		if p == "" {
			continue
		}
		if !first {
			out = append(out, ';')
		}
		first = false
		if needsEscape(p) >= 0 {
			out = append(out, htmlEscapeBytes(p)...)
		} else {
			out = append(out, p...)
		}
	}
	return &attrNode{name: "style", value: string(out)}
}

// Selected emits the boolean `selected` attribute.
func Selected() H { return buildBool("selected") }

// Checked emits the boolean `checked` attribute.
func Checked() H { return buildBool("checked") }

// Required emits the boolean `required` attribute.
func Required() H { return buildBool("required") }

// Disabled emits the boolean `disabled` attribute.
func Disabled() H { return buildBool("disabled") }

// Class joins non-empty class names with spaces and emits a single
// class attribute. Returns nil when no class names remain so the
// attribute is omitted entirely.
//
//	h.Class("btn")                                  // single
//	h.Class("btn", "primary")                       // many
//	h.Class("btn", h.IfStr(active, "active"))       // conditional
func Class(parts ...string) H {
	total := 0
	count := 0
	for _, p := range parts {
		if p == "" {
			continue
		}
		if count > 0 {
			total++
		}
		total += len(p)
		count++
	}
	if count == 0 {
		return nil
	}
	out := make([]byte, 0, total)
	first := true
	for _, p := range parts {
		if p == "" {
			continue
		}
		if !first {
			out = append(out, ' ')
		}
		first = false
		if needsEscape(p) >= 0 {
			out = append(out, htmlEscapeBytes(p)...)
		} else {
			out = append(out, p...)
		}
	}
	return &attrNode{name: "class", value: string(out)}
}

// Classes is an alias for [Class] retained so a slice already in hand
// can be spread without a rename. `Class(parts...)` is equivalent.
//
// Deprecated: use [Class]. Classes will be removed in a future major
// release.
func Classes(parts ...string) H { return Class(parts...) }

// ClassMap renders a class attribute that includes each key whose value
// is true. Keys are emitted in sorted order so the output is stable
// across renders.
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
	return Class(keys...)
}

// IfStr returns s if cond is true, "" otherwise. Pairs with [Class]
// and [Styles] for inline conditional fragments.
func IfStr(cond bool, s string) string {
	if cond {
		return s
	}
	return ""
}
