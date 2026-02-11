package h

import (
	"testing"

	"github.com/stretchr/testify/assert"
	g "maragu.dev/gomponents"
)

func TestRetype(t *testing.T) {
	t.Run("empty slice", func(t *testing.T) {
		result := retype([]H{})
		assert.Nil(t, result)
	})

	t.Run("with nodes", func(t *testing.T) {
		nodes := []H{Text("test1"), Text("test2")}
		result := retype(nodes)
		assert.Len(t, result, 2)
		for _, node := range result {
			assert.Implements(t, (*g.Node)(nil), node)
		}
	})

	t.Run("with nils", func(t *testing.T) {
		nodes := []H{Text("test1"), nil, Text("test2")}
		result := retype(nodes)
		assert.Len(t, result, 3)
		assert.NotNil(t, result[0])
		assert.Nil(t, result[1])
		assert.NotNil(t, result[2])
	})
}
