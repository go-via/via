package demos

import (
	"fmt"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
	"github.com/go-via/via/on"
)

// hookLogs outlives every instance: the GET's, the stream's, and the one a
// closed stream disposed of each write here, and none of them lives long enough
// to see the others' lines.
var hookLogs = &sessionLog{}

type sessionLog struct {
	mu sync.Mutex
	by map[string][]string
}

const (
	stampFormat     = "15:04:05.000"
	hookLogRows     = 14
	hookLogSessions = 2000
)

func (s *sessionLog) add(sid string, lines ...string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.by == nil || len(s.by) >= hookLogSessions {
		s.by = map[string][]string{}
	}
	// Instances append out of order (a request that ran before the session
	// existed hands its lines over later), and every line starts with its time.
	log := append(s.by[sid], lines...)
	slices.SortStableFunc(log, func(a, b string) int { return strings.Compare(a[:len(stampFormat)], b[:len(stampFormat)]) })
	if len(log) > hookLogRows {
		log = log[len(log)-hookLogRows:]
	}
	s.by[sid] = log
	return slices.Clone(log)
}

var hookInstances atomic.Int64

// HookLog writes one line per hook, tagged with the instance it ran on. Until
// the visitor has a session there is nowhere to keep lines across instances, so
// each instance shows only its own; the first action mints one.
type HookLog struct {
	Log via.List[string]
	n   int64
	sid string
}

func (l *HookLog) OnInit(ctx *via.Ctx) error {
	l.n = hookInstances.Add(1)
	l.sid = ctx.Session().ID()
	l.note("OnInit", ctx.Request().Method+" "+ctx.Request().URL.Path)
	ctx.OnConnect(l.connected)
	ctx.OnDispose(l.disposed)
	return nil
}

func (l *HookLog) connected() { l.note("OnConnect", "stream open") }
func (l *HookLog) disposed()  { l.note("OnDispose", "stream closed") }

func (l *HookLog) Act(ctx *via.Ctx) {
	if l.sid == "" {
		l.sid = ctx.Session().Ensure()
		hookLogs.add(l.sid, l.Log.Get()...)
	}
	l.note("action", "Act")
}

func (l *HookLog) OnReload(ctx *via.Ctx) error {
	l.note("OnReload", "after Act")
	return nil
}

func (l *HookLog) note(hook, detail string) {
	line := fmt.Sprintf("%s  #%d  %-9s  %s", time.Now().Format(stampFormat), l.n, hook, detail)
	if l.sid == "" {
		l.Log.Append(line)
		trim(&l.Log, hookLogRows)
		return
	}
	l.Log.Set(hookLogs.add(l.sid, line))
}

func (l *HookLog) View() h.H {
	return h.Div(
		h.Button(on.Click(l.Act), h.Str("run an action")),
		h.Ul(h.Class("loglist"), h.TabIndex(0), h.Role("log"), h.Aria("label", "Hook calls"),
			l.Log.Each(func(s string) h.H { return h.Li(h.Str(s)) })),
	)
}
