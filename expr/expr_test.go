package expr_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/go-via/via/expr"
)

func TestExpr_emitsComposedExpressions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  expr.Expr
		want string
	}{
		{"not", expr.Expr("$open").Not(), "(!$open)"},
		{"eq literal", expr.Expr("$name").Eq("ann"), `($name === "ann")`},
		{"eq expr", expr.Expr("$a").Eq(expr.Expr("$b")), "($a === $b)"},
		{"ne", expr.Expr("$q").Ne(""), `($q !== "")`},
		{"lt", expr.Expr("$n").Lt(3), "($n < 3)"},
		{"le", expr.Expr("$n").Le(3), "($n <= 3)"},
		{"gt", expr.Expr("$n").Gt(3), "($n > 3)"},
		{"ge", expr.Expr("$n").Ge(3), "($n >= 3)"},
		{"assign", expr.Expr("$n").Assign(1), "$n = 1"},
		{"toggle", expr.Expr("$open").Toggle(), "$open = !$open"},
		{"add", expr.Expr("$n").Add(-1), "$n += -1"},
		{"all", expr.All(expr.Expr("$a"), expr.Expr("$b")), "($a && $b)"},
		{"any", expr.Any(expr.Expr("$a"), expr.Expr("$b"), expr.Expr("$c")), "($a || $b || $c)"},
		{"do", expr.Do(expr.Expr("$a").Toggle(), expr.Expr("$n").Add(1)), "$a = !$a; $n += 1"},
		{"val", expr.Val([]int{1, 2}), "[1,2]"},
		{"call", expr.Call("drawChart", expr.El, expr.Expr("$series")), "drawChart(el, $series)"},
		{"call dotted, no args", expr.Call("console.log"), "console.log()"},
		{"copy literal", expr.CopyToClipboard(expr.Val("go get github.com/go-via/via")), `navigator.clipboard.writeText("go get github.com/go-via/via").catch(() => {})`},
		{"copy el-relative", expr.CopyToClipboard(expr.Raw("el.dataset.copy")), "navigator.clipboard.writeText(el.dataset.copy).catch(() => {})"},
		{"copy text", expr.CopyTextOf("pre"), `navigator.clipboard.writeText(el.parentElement?.querySelector("pre")?.textContent ?? "").catch(() => {})`},
		{"class on", expr.Class("copied", true), `el.classList.toggle("copied", true)`},
		{"class off", expr.Class("copied", false), `el.classList.toggle("copied", false)`},
		{"raw", expr.Raw("$n > 0 ? 1 : 2"), "$n > 0 ? 1 : 2"},
		{"el", expr.El, "el"},
		{"nested", expr.All(expr.Expr("$a").Not(), expr.Expr("$n").Ge(2)), "((!$a) && ($n >= 2))"},
		{"all parenthesizes an assignment", expr.All(expr.Raw("evt.key").Eq("Escape"), expr.Expr("$q").Assign("")), `((evt.key === "Escape") && ($q = ""))`},
		{"any parenthesizes a looser operand", expr.Any(expr.Raw("$a && $b"), expr.Expr("$c")), "(($a && $b) || $c)"},
		{"two groups are not one", expr.All(expr.Raw("($a) || ($b)"), expr.Raw("$c")), "((($a) || ($b)) && $c)"},
		{"literal operand stays bare", expr.All(expr.Val(true), expr.Val(`a)`)), `(true && "a)")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.got.String())
		})
	}
}

func TestVal_escapesAtSignsDatastarWouldRewrite(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		got  expr.Expr
		want string
	}{
		{"plain string unchanged", expr.Val("hello"), `"hello"`},
		{"dollar unchanged", expr.Val("$5"), `"$5"`},
		{"at sign escaped", expr.Val("@post("), `"\u0040post("`},
		{"at sign in a slice escaped", expr.Val([]string{"@get("}), `["\u0040get("]`},
		{"operand escaped", expr.Expr("$q").Eq("@x"), `($q === "\u0040x")`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.got.String())
		})
	}
}

func TestLit_matchesVal(t *testing.T) {
	t.Parallel()
	assert.Equal(t, expr.Val("send @post('/x')"), expr.Lit("send @post('/x')"))
}

func TestAllAny_returnASingleArgumentUnchanged(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "$a", expr.All(expr.Expr("$a")).String())
	assert.Equal(t, "$a", expr.Any(expr.Expr("$a")).String())
}

func TestExpr_panicsOnUnencodableLiteral(t *testing.T) {
	t.Parallel()
	assert.PanicsWithValue(t,
		"expr: chan int does not encode as a JSON literal",
		func() { expr.Expr("$n").Eq(make(chan int)) })
}

func TestExpr_panicsOnMutationOfAComposedExpression(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		fn   func()
	}{
		{"assign", func() { expr.Expr("($a && $b)").Assign(1) }},
		{"toggle", func() { expr.Expr("$a.b").Toggle() }},
		{"add", func() { expr.Expr("count").Add(1) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, tt.fn)
		})
	}
}

func TestCall_panicsOnABadName(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"", "draw chart", "draw()", "a..b", "1draw"} {
		assert.Panics(t, func() { expr.Call(name) }, name)
	}
}

func TestAllAny_panicOnNoArguments(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { expr.All() })
	assert.Panics(t, func() { expr.Any() })
}

func TestRawf_splicesCheckedExpressionsIntoUncheckedText(t *testing.T) {
	t.Parallel()
	got := expr.Rawf("const s=%s;s.forEach((v,i)=>%s.lineTo(i,v))",
		expr.Expr("$load"), expr.El)
	assert.Equal(t, "const s=$load;s.forEach((v,i)=>el.lineTo(i,v))", got.String())
}

func TestRawf_emitsALiteralPercentForDoublePercent(t *testing.T) {
	t.Parallel()
	got := expr.Rawf("%s %% 2", expr.Expr("$n"))
	assert.Equal(t, "$n % 2", got.String())
}

func TestRawf_panicsOnAVerbOtherThanS(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		format string
	}{
		{"d verb", "%d"},
		{"v verb", "%v"},
		{"trailing lone percent", "%"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Panics(t, func() { expr.Rawf(tt.format, expr.Expr("$n")) })
		})
	}
}

func TestRawf_panicsOnAnArgumentCountMismatch(t *testing.T) {
	t.Parallel()
	assert.Panics(t, func() { expr.Rawf("%s %s", expr.Expr("$a")) })
	assert.Panics(t, func() { expr.Rawf("%s", expr.Expr("$a"), expr.Expr("$b")) })
}
