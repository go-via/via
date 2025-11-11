package main

import (
	"fmt"
	"sync"
	"time"
)

type userHasId interface {
	comparable
	getUserId() string
}
type Syncable interface {
	Sync()
}
type UserAndSync[TR any, TU userHasId] struct {
	user *TU
	sync Syncable
}
type Rooms[TR any, TU userHasId] struct {
	byName map[string]*Room[TR, TU]
	names  []string
}

func (rs *Rooms[TR, TU]) Visit(fn func(n string)) {
	for _, n := range rs.names {
		fn(n)
	}
}

func (rs *Rooms[TR, TU]) Get(n string) (*Room[TR, TU], bool) {
	rm, ok := rs.byName[n]
	return rm, ok
}

func (rs *Rooms[TR, TU]) Start() {
	for _, rm := range rs.byName {
		go rm.run()
	}
}

func (rs *Rooms[TR, TU]) Stop() {
	for _, rm := range rs.byName {
		rm.stop()
	}
}

// NewRooms seeds the rooms once at startup.
// Assumptions: rooms don't change. Should be sorted by name.
func NewRooms[TR any, TU userHasId](names ...string) Rooms[TR, TU] {
	byName := make(map[string]*Room[TR, TU])
	for _, n := range names {
		byName[n] = NewRoom[TR, TU](n)
	}

	return Rooms[TR, TU]{byName, names}
}

type Room[TR any, TU userHasId] struct {
	data        TR
	dataMu      sync.RWMutex
	members     map[TU]Syncable
	membersMu   sync.RWMutex
	Name        string
	join        chan *UserAndSync[TR, TU]
	leave       chan *TU
	done        chan struct{}
	stopChannel chan struct{}
	dirty       bool
}

// UpdateData lets the calling function update the room data.
// Is called with a write lock - so should be *fast*
func (r *Room[TR, TU]) UpdateData(fn func(data *TR)) {
	r.dataMu.Lock()
	defer r.dataMu.Unlock()
	fn(&r.data)
	r.dirty = true
}

func (r *Room[TR, TU]) Publish() {
	r.dataMu.Lock()
	if !r.dirty {
		r.dataMu.Unlock()
		return
	}

	publishers := make([]Syncable, 0, len(r.members))
	for _, sync := range r.members {
		publishers = append(publishers, sync)
	}
	r.dirty = false
	r.dataMu.Unlock()

	// Now call Sync without holding the lock
	for _, sync := range publishers {
		sync.Sync()
	}
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

func (r *Room[TR, TU]) Join(us *UserAndSync[TR, TU]) {
	r.join <- us
}

func (r *Room[TR, TU]) Leave(u *TU) {
	// fmt.Println("Pushing", u, "to leave channel")
	r.leave <- u
}

func (r *Room[TR, TU]) MemberCount() int {
	r.membersMu.RLock()
	defer r.membersMu.RUnlock()
	return len(r.members)
}

func NewRoom[TR any, TU userHasId](n string) *Room[TR, TU] {
	return &Room[TR, TU]{
		Name:        n,
		join:        make(chan *UserAndSync[TR, TU], 5),
		leave:       make(chan *TU, 5),
		stopChannel: make(chan struct{}),
		done:        make(chan struct{}),
		members:     make(map[TU]Syncable),
	}
}

func (r *Room[TR, TU]) run() {
	defer close(r.done)
	publishTicker := time.NewTicker(400 * time.Millisecond)
	defer publishTicker.Stop()
	for {
		select {
		case usrAndSync := <-r.join:
			fmt.Println("Joining: ", *usrAndSync.user)
			r.membersMu.Lock()
			r.members[*usrAndSync.user] = usrAndSync.sync
			r.membersMu.Unlock()
		case usr := <-r.leave:
			fmt.Println("Leaving: ", *usr)
			r.membersMu.Lock()
			delete(r.members, *usr)
			r.membersMu.Unlock()
		case <-publishTicker.C:
			r.Publish()
		case <-r.stopChannel:
			return // exit goroutine
		}
	}
}

func (r *Room[TR, TU]) stop() {
	close(r.stopChannel)
	<-r.done // wait for run() to finish
}
