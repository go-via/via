package core_test

import (
	"testing"

	"github.com/go-via/viashowcase/internal/core"
	"github.com/stretchr/testify/assert"
)

func TestPollChoices(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"trims surrounding spaces on each choice", "Pizza, Sushi, Tacos", []string{"Pizza", "Sushi", "Tacos"}},
		{"drops blank and whitespace-only entries, preserving order", " ,Pizza,  ,Sushi, ", []string{"Pizza", "Sushi"}},
		{"empty input yields no choices", "", nil},
		{"all-blank input yields no choices", "  , , ", nil},
		{"single choice with no commas", "Pizza", []string{"Pizza"}},
		{"interior spaces within a choice are preserved", "Deep dish, Thin crust", []string{"Deep dish", "Thin crust"}},
		{"duplicate choices are preserved, not deduped", "Pizza, Pizza", []string{"Pizza", "Pizza"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, core.PollChoices(tt.raw))
		})
	}
}
