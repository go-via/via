package islands

import (
	"sync"
	"time"

	"github.com/go-via/via"
)

// snippet:start dispose
// Prices is an island fed by one producer that every open tab shares. The
// stream opening claims it; the stream closing releases it.
type Prices struct {
	Series via.Signal[[]int] `via:"init=[]"`
}

func (p *Prices) OnInit(ctx *via.Ctx) error {
	ctx.Tick(time.Second, p.poll)
	ctx.OnConnect(feed.Acquire)
	ctx.OnDispose(feed.Release)
	return nil
}

func (p *Prices) poll(ctx *via.Ctx) { p.Series.Set(feed.Last()) }

// snippet:end

// producer counts the tabs holding it; a real one starts on the first
// Acquire and stops on the last Release.
type producer struct {
	mu    sync.Mutex
	users int
	last  []int
}

var feed = &producer{}

func (f *producer) Acquire() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users++
}

func (f *producer) Release() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.users--
}

func (f *producer) Last() []int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]int(nil), f.last...)
}
