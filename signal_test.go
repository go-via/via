package via

import (
	//	"net/http/httptest"
	"testing"

	"github.com/go-via/via/h"
	"github.com/stretchr/testify/assert"
)

func TestSignalReturnAsString(t *testing.T) {
	testcases := []struct {
		desc     string
		given    any
		expected string
	}{
		{"string", "test", "test"},
		{"other string", "another", "another"},
		{"int", 1, "1"},
		{"negative int", -99, "-99"},
		{"float", 1.1, "1.1"},
		{"negative float", -34.345, "-34.345"},
		{"positive bool", true, "true"},
		{"negative bool", false, "false"},
	}

	for _, testcase := range testcases {
		t.Run(testcase.desc, func(t *testing.T) {
			t.Parallel()
			var sig *signal
			v := New()
			v.Page("/", func(c *Context) {
				sig = c.Signal(testcase.given)
				c.View(func() h.H { return h.Div() })
			})
			assert.Equal(t, testcase.expected, sig.String())
		})

	}
}

func TestSignalReturnAsStringComplexTypes(t *testing.T) {
	testcases := []struct {
		desc     string
		given    any
		expected string
	}{
		{"string slice", []string{"test"}, `["test"]`},
		{"int slice", []int{1, 2}, "[1, 2]"},
		{"struct1", struct{ Val string }{"test"}, `{"Val": "test"}`},
		{"struct2", struct {
			Num        int
			IsPositive bool
		}{1, true}, `{"Num": 1, "IsPositive": true}`},
	}

	for _, testcase := range testcases {
		t.Run(testcase.desc, func(t *testing.T) {
			t.Parallel()
			var sig *signal
			v := New()
			v.Page("/", func(c *Context) {
				c.View(func() h.H { return nil })
				sig = c.Signal(testcase.given)
			})
			assert.JSONEq(t, testcase.expected, sig.String())
		})
	}
}
