package main

import (
	"fmt"
	"sync"
)

type userHasId interface {
	comparable
	getUserId() string
}
type Rooms[TR any, TU userHasId] struct {
	byName map[string]Room[TR, TU]
	names  []string
}

func (rs *Rooms[TR, TU]) Visit(fn func(n string)) {
	for _, n := range rs.names {
		fn(n)
	}
}

func (rs *Rooms[TR, TU]) Get(n string) (*Room[TR, TU], bool) {
	rm, ok := rs.byName[n]
	return &rm, ok
}

// NewRooms seeds the rooms once at startup.
// Assumptions: rooms don't change. Should be sorted by name.
func NewRooms[TR any, TU userHasId](names ...string) Rooms[TR, TU] {
	byName := make(map[string]Room[TR, TU])
	for _, n := range names {
		byName[n] = *NewRoom[TR, TU](n)
	}

	return Rooms[TR, TU]{byName, names}
}

type Room[TR any, TU userHasId] struct {
	data      TR
	dataMu    sync.RWMutex
	members   map[TU]struct{}
	membersMu sync.RWMutex
	Name      string
	join      chan *TU
	leave     chan *TU
	done      chan struct{}
	stop      chan struct{}
	dirty     bool
}

// UpdateData lets the calling function update the room data.
// Is called with a write lock - so should be *fast*
func (r *Room[TR, TU]) UpdateData(fn func(data *TR)) {
	r.dataMu.Lock()
	defer r.dataMu.Unlock()
	fn(&r.data)
	r.dirty = true
	fmt.Println(r.data)
}

// Get room data. This is a copy.
func (r *Room[TR, TU]) GetData() TR {
	r.dataMu.RLock()
	defer r.dataMu.RUnlock()
	return r.data
}

func (r *Room[TR, TU]) Dirty() bool {
	return r.dirty
}

func (r *Room[TR, TU]) Join(u TU) {
	r.join <- &u
}

func (r *Room[TR, TU]) Leave(u TU) {
	// fmt.Println("Pushing", u, "to leave channel")
	r.leave <- &u
}

func (r *Room[TR, TU]) MemberCount() int {
	r.membersMu.RLock()
	defer r.membersMu.RUnlock()
	return len(r.members)
}

func NewRoom[TR any, TU userHasId](n string) *Room[TR, TU] {
	return &Room[TR, TU]{
		Name:    n,
		join:    make(chan *TU, 10), // MUST have a size here or will block.
		leave:   make(chan *TU, 10), // MUST have a size here or will block.
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		members: make(map[TU]struct{}),
	}
}

func (r *Room[TR, TU]) Start() {
	go r.run()
}

func (r *Room[TR, TU]) run() {
	defer close(r.done)
	for {
		select {
		case usr := <-r.join:
			r.membersMu.Lock()
			r.members[*usr] = struct{}{}
			r.membersMu.Unlock()
		case usr := <-r.leave:
			r.membersMu.Lock()
			delete(r.members, *usr)
			r.membersMu.Unlock()
		case <-r.stop:
			return // exit goroutine
		}
	}
}

func (r *Room[TR, TU]) Stop() {
	close(r.stop)
	<-r.done // wait for run() to finish
}
