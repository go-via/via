// Package expr composes the small JavaScript expressions Datastar evaluates in
// the browser: a signal reference like "$count", a comparison, an assignment, a
// call. It emits expressions and nothing else; h's Data* functions turn one
// into an attribute. It imports nothing from via or h.
//
// Everything here is checked except [Raw] and the text of [Rawf], which are
// emitted verbatim.
package expr

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Expr is a Datastar client expression. A bare "$name" one is a signal
// reference, which is what Signal.Ref returns; the methods and functions here
// build larger expressions out of those.
type Expr string

// String returns the expression source.
func (e Expr) String() string { return string(e) }

// Not is JavaScript's !, parenthesized.
func (e Expr) Not() Expr { return "(!" + e + ")" }

// Eq compares with JavaScript's strict ===.
func (e Expr) Eq(v any) Expr { return e.binary("===", v) }

// Ne compares with JavaScript's strict !==.
func (e Expr) Ne(v any) Expr { return e.binary("!==", v) }

// Lt is <.
func (e Expr) Lt(v any) Expr { return e.binary("<", v) }

// Le is <=.
func (e Expr) Le(v any) Expr { return e.binary("<=", v) }

// Gt is >.
func (e Expr) Gt(v any) Expr { return e.binary(">", v) }

// Ge is >=.
func (e Expr) Ge(v any) Expr { return e.binary(">=", v) }

// Assign writes v to the signal. The receiver must be a bare $name.
func (e Expr) Assign(v any) Expr {
	e.mustBeRef("Assign")
	return e + " = " + Expr(operand(v))
}

// Toggle negates the signal in place. The receiver must be a bare $name.
func (e Expr) Toggle() Expr {
	e.mustBeRef("Toggle")
	return e + " = !" + e
}

// Add adds v to the signal in place. The receiver must be a bare $name.
func (e Expr) Add(v any) Expr {
	e.mustBeRef("Add")
	return e + " += " + Expr(operand(v))
}

func (e Expr) binary(op string, v any) Expr {
	return "(" + e + " " + Expr(op) + " " + Expr(operand(v)) + ")"
}

var bareRef = regexp.MustCompile(`^\$[A-Za-z_][A-Za-z0-9_]*$`)

func (e Expr) mustBeRef(op string) {
	if !bareRef.MatchString(string(e)) {
		panic(fmt.Sprintf("expr: %s needs a bare $name receiver, got %q", op, string(e)))
	}
}

// operand splices an Expr as-is and encodes anything else as a JSON literal,
// which is also a JavaScript literal for every type json can reach.
func operand(v any) string {
	if e, ok := v.(Expr); ok {
		return string(e)
	}
	b, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("expr: %T does not encode as a JSON literal", v))
	}
	return string(b)
}

// All joins the expressions with &&. A single one is returned unchanged; none
// panics.
func All(es ...Expr) Expr { return join("All", "&&", es) }

// Any joins the expressions with ||, with the same single/none rule as [All].
func Any(es ...Expr) Expr { return join("Any", "||", es) }

func join(name, op string, es []Expr) Expr {
	if len(es) == 0 {
		panic("expr: " + name + " needs at least one expression")
	}
	if len(es) == 1 {
		return es[0]
	}
	return "(" + Expr(strings.Join(sources(es), " "+op+" ")) + ")"
}

// Do sequences statements, for an attribute that runs more than one.
func Do(es ...Expr) Expr { return Expr(strings.Join(sources(es), "; ")) }

// Lit encodes v as a JavaScript literal; an Expr passes through unchanged.
func Lit(v any) Expr { return Expr(operand(v)) }

var callName = regexp.MustCompile(`^[A-Za-z_$][\w$]*(\.[A-Za-z_$][\w$]*)*$`)

// Call applies a function by name, which may be a dotted path
// ("console.log"). An invalid name panics.
func Call(name string, args ...Expr) Expr {
	if !callName.MatchString(name) {
		panic(fmt.Sprintf("expr: %q is not a function name", name))
	}
	return Expr(name + "(" + strings.Join(sources(args), ", ") + ")")
}

// El is the element the attribute is written on.
const El Expr = "el"

// Raw emits js verbatim and unchecked. Never build one from user input.
func Raw(js string) Expr { return Expr(js) }

// Rawf splices checked expressions into unchecked text: each %s takes the next
// Expr verbatim, %% is a literal percent, and any other verb or a wrong
// argument count panics. The text itself is emitted as written, like Raw —
// never build one from user input; the args are the only part expr vouches for.
func Rawf(format string, args ...Expr) Expr {
	var b strings.Builder
	i := 0
	for j := 0; j < len(format); j++ {
		c := format[j]
		if c != '%' {
			b.WriteByte(c)
			continue
		}
		j++
		if j == len(format) {
			panic("expr: Rawf takes %s and %% only, got a trailing %")
		}
		switch format[j] {
		case 's':
			if i == len(args) {
				panic(fmt.Sprintf("expr: Rawf has %d %%s verbs and %d arguments",
					strings.Count(format, "%s"), len(args)))
			}
			b.WriteString(string(args[i]))
			i++
		case '%':
			b.WriteByte('%')
		default:
			// fmt.Sprintf would answer a bad verb with "%!d(...)" instead of
			// panicking, so the scan is hand-written to enforce %s-or-%%-only.
			panic(fmt.Sprintf("expr: Rawf takes %%s and %%%% only, got %%%c", format[j]))
		}
	}
	if i != len(args) {
		panic(fmt.Sprintf("expr: Rawf has %d %%s verbs and %d arguments",
			strings.Count(format, "%s"), len(args)))
	}
	return Expr(b.String())
}

func sources(es []Expr) []string {
	out := make([]string, len(es))
	for i, e := range es {
		out[i] = string(e)
	}
	return out
}
