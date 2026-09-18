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
		{"lit", expr.Lit([]int{1, 2}), "[1,2]"},
		{"call", expr.Call("drawChart", expr.El, expr.Expr("$series")), "drawChart(el, $series)"},
		{"call dotted, no args", expr.Call("console.log"), "console.log()"},
		{"raw", expr.Raw("$n > 0 ? 1 : 2"), "$n > 0 ? 1 : 2"},
		{"el", expr.El, "el"},
		{"nested", expr.All(expr.Expr("$a").Not(), expr.Expr("$n").Ge(2)), "((!$a) && ($n >= 2))"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, tt.got.String())
		})
	}
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
