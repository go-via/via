package via

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unmarshalableEvent has a channel field, which encoding/json cannot marshal —
// the cheapest way to drive marshalEvent's payload-encode error path.
type unmarshalableEvent struct{ Ch chan int }

func (unmarshalableEvent) Fold(acc int, _ unmarshalableEvent) int { return acc }

// A failed event encode is surfaced to StateAppEvents.Append's caller, so the
// error must name its origin ("via:") — a bare "json: unsupported type" leaves
// an operator with no clue the failure came from committing an event.
func TestMarshalEvent_errorNamesViaOrigin(t *testing.T) {
	t.Parallel()
	app := &App{cfg: config{}}
	_, err := marshalEvent(app, unmarshalableEvent{Ch: make(chan int)})
	require.Error(t, err)
	assert.Truef(t, strings.HasPrefix(err.Error(), "via:"),
		"marshalEvent error must carry a via: origin prefix, got %q", err.Error())
}
