package via

import (
	"net/http"
	"sync/atomic"
)

type Ctx struct {
	id           string
	routeParams []string
	session      *session
	doneChan     chan struct{}
	lastAccess   atomic.Int64

	w http.ResponseWriter
	r *http.Request
}

func (ctx *Ctx) touch() {
	ctx.lastAccess.Store(1)
}

func (ctx *Ctx) Done() <-chan struct{} {
	return ctx.doneChan
}
