// Package storetest checks the stores the counter demos share between tabs. It
// is its own package because a _test.go in demos would be embedded and served.
package storetest

import (
	"sync"
	"testing"

	"github.com/go-via/via"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go-via.dev/site/demos"
)

type allowAll struct{}

func (allowAll) Allow(*via.Ctx) bool { return true }

// hammer clicks from many tabs at once. Each goroutine gets its own unit, as
// each tab does: via never runs one unit's callbacks concurrently.
func hammer(tab func() (inc, dec func(*via.Ctx))) {
	var wg sync.WaitGroup
	for g := range 32 {
		inc, dec := tab()
		wg.Go(func() {
			for i := range 200 {
				if (g+i)%3 == 0 {
					dec(nil)
				} else {
					inc(nil)
				}
			}
		})
	}
	wg.Wait()
}

func TestStartCounter_publishesTheFinalCountLast(t *testing.T) {
	demos.ResetAll()
	moved, load := demos.StartStore()
	sub := moved.Subscribe()
	defer sub.Stop()

	hammer(func() (inc, dec func(*via.Ctx)) {
		c := demos.NewStartCounter(allowAll{})
		return c.Inc, c.Dec
	})

	got, _ := sub.Drain()
	require.NotEmpty(t, got)
	assert.Equal(t, load(), got[len(got)-1])
}

func TestShared_publishesTheFinalCountLast(t *testing.T) {
	demos.ResetAll()
	moved, load := demos.SharedStore()
	sub := moved.Subscribe()
	defer sub.Stop()

	hammer(func() (inc, dec func(*via.Ctx)) {
		s := demos.NewShared(allowAll{})
		return s.Inc, s.Inc
	})

	got, _ := sub.Drain()
	require.NotEmpty(t, got)
	assert.Equal(t, load(), got[len(got)-1])
}

func TestLivePresence_publishesTheFinalCountLast(t *testing.T) {
	moved, load, add := demos.PresenceStore()
	sub := moved.Subscribe()
	defer sub.Stop()

	hammer(func() (join, leave func(*via.Ctx)) {
		return func(*via.Ctx) { add(1) }, func(*via.Ctx) { add(-1) }
	})

	got, _ := sub.Drain()
	require.NotEmpty(t, got)
	assert.Equal(t, load(), got[len(got)-1])
}

func TestTutorialChat_publishesTheFinalHeadCountLast(t *testing.T) {
	moved, load, add := demos.ChatPresenceStore()
	sub := moved.Subscribe()
	defer sub.Stop()

	hammer(func() (join, leave func(*via.Ctx)) {
		return func(*via.Ctx) { add(1) }, func(*via.Ctx) { add(-1) }
	})

	got, _ := sub.Drain()
	require.NotEmpty(t, got)
	assert.Equal(t, load(), got[len(got)-1])
}

func TestResetAll_zeroesEveryCounter(t *testing.T) {
	c := demos.NewStartCounter(allowAll{})
	s := demos.NewShared(allowAll{})
	l := demos.NewLandingCounter()
	c.Inc(nil)
	s.Inc(nil)
	l.Inc(nil)

	demos.ResetAll()

	_, startLoad := demos.StartStore()
	_, sharedLoad := demos.SharedStore()
	assert.Zero(t, startLoad())
	assert.Zero(t, sharedLoad())
	assert.Zero(t, demos.LandingCount())
}
