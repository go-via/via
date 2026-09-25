package demos

import "github.com/go-via/via"

// No card shows this file. It holds what the demo files use without declaring;
// copy it with any demo you take.

// Limiter is an interface, not *demo.Limiter: demo embeds this package's
// source, so this package cannot import it back.
type Limiter interface{ Allow(ctx *via.Ctx) bool }

const keepRows = 50

func trim[T any](l *via.List[T], n int) {
	if v := l.Get(); len(v) > n {
		l.Set(v[len(v)-n:])
	}
}
