package via

import (
	//	"net/http/httptest"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func TestSignalReturnAsString(t *testing.T) {
	testcases := []struct {
		given    any
		expected string
	}{
		{"test", "test"},
		{"another", "another"},
		{1, "1"},
		{-99, "-99"},
		{1.1, "1.1"},
		{-34.345, "-34.345"},
		{true, "true"},
		{false, "false"},
	}

	for _, testcase := range testcases {
		var sig *signal
		v := New()
		v.Page("/", func(c *Context) {
			c.View(func() h.H { return nil })
			sig = c.Signal(testcase.given)
		})

		assert.Equal(t, testcase.expected, sig.String())

	}
}

func TestSignalReturnAsStringComplexTypes(t *testing.T) {
	testcases := []struct {
		given    any
		expected string
	}{
		{[]string{"test"}, `["test"]`},
		{[]int{1, 2}, "[1, 2]"},
		{struct{ Val string }{"test"}, `{"Val": "test"}`},
		{struct {
			Num        int
			IsPositive bool
		}{1, true}, `{"Num": 1, "IsPositive": true}`},
	}

	for _, testcase := range testcases {
		var sig *signal
		v := New()
		v.Page("/", func(c *Context) {
			c.View(func() h.H { return nil })
			sig = c.Signal(testcase.given)
		})

		assert.JSONEq(t, testcase.expected, sig.String())

	}
}
