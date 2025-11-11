package main

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Room manages generic state and broadcasts updates to registered sync functions.
// It tracks dirty state and automatically broadcasts to all registered contexts when updated.
//
// Note: Without a disconnect hook from Via, registered sync functions from dead SSE
// connections may accumulate. This is harmless since c.Sync() returns early when SSE is nil,
// but over long-running applications with many short-lived connections, memory usage will grow.
type Room[T any] struct {
	mu      sync.RWMutex
	data    T
	dirty   bool
	syncFns map[int]func() // Changed from []func()
	nextID  int
	roomID  string
}

// RegisterWithCleanup adds a sync function and returns a cleanup function
// that removes it from the room's syncFns list. This prevents memory leaks
// when switching between rooms.
func (r *Room[T]) RegisterWithCleanup(syncFn func()) func() {
	r.mu.Lock()
	if r.syncFns == nil {
		r.syncFns = make(map[int]func())
	}
	id := r.nextID
	r.nextID++
	r.syncFns[id] = syncFn
	r.mu.Unlock()

	return func() {
		r.mu.Lock()
		defer r.mu.Unlock()
		delete(r.syncFns, id)
	}
}

func (r *Room[T]) SyncCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.syncFns)
}

// Read allows reading the room's data under a read lock.
func (r *Room[T]) Read(fn func(*T)) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	fn(&r.data)
}

// GetData returns a copy of the room's data.
func (r *Room[T]) GetData() T {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.data
}

// Write modifies the room's data using the provided function and marks the room as dirty.
// The next broadcast tick will push updates to all registered sync functions.
func (r *Room[T]) Write(fn func(*T)) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fn(&r.data)
	r.dirty = true
}

// MarkDirty marks the room as dirty without modifying data.
// The next broadcast tick will push updates to all registered sync functions.
func (r *Room[T]) MarkDirty() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.dirty = true
}

type broadcaster interface {
	broadcast()
}

func (r *Room[T]) broadcast() {
	r.mu.Lock()
	if !r.dirty || len(r.syncFns) == 0 {
		r.mu.Unlock()
		return
	}

	// Copy sync functions to call them outside the lock.
	fns := make([]func(), 0, len(r.syncFns))
	for _, fn := range r.syncFns {
		fns = append(fns, fn)
	}
	r.dirty = false
	r.mu.Unlock()

	for _, fn := range fns {
		fn()
	}
}

// Rooms manages multiple Room instances, each identified by a roomID.
type Rooms struct {
	mu        sync.RWMutex
	rooms     map[string]any
	sortedIds []string
}

// NewRooms creates a Rooms manager and starts a background goroutine that broadcasts
// updates to dirty rooms every 500ms.
//
// Note: The background goroutine runs for the lifetime of the process. Since Via's
// Start() uses log.Fatalf(), there's no graceful shutdown mechanism, so the goroutine
// will terminate when the process exits.
func NewRooms() *Rooms {
	rs := &Rooms{rooms: make(map[string]any)}
	go rs.loop()
	return rs
}

func (rs *Rooms) loop() {
	t := time.NewTicker(250 * time.Millisecond)
	for range t.C {
		rs.mu.RLock()
		list := make([]broadcaster, 0, len(rs.rooms))
		for _, r := range rs.rooms {
			if b, ok := r.(broadcaster); ok {
				list = append(list, b)
			}
		}
		rs.mu.RUnlock()

		for _, b := range list {
			b.broadcast()
		}
	}
}

// GetRoom returns a typed Room for the given roomID. If the room doesn't exist,
// it creates one with the initial data. If a room with the same ID exists but has a
// different type, it panics.
func GetRoom[T any](rs *Rooms, roomID string, initial T, createIfNotFound bool) *Room[T] {
	rs.mu.RLock()
	if existing, ok := rs.rooms[roomID]; ok {
		rs.mu.RUnlock()
		if typed, ok := existing.(*Room[T]); ok {
			return typed
		}
		panic(fmt.Sprintf("via: room %s exists with a different type", roomID))
	}
	rs.mu.RUnlock()

	if !createIfNotFound {
		return nil
	}

	rs.mu.Lock()
	defer rs.mu.Unlock()
	if existing, ok := rs.rooms[roomID]; ok {
		if typed, ok := existing.(*Room[T]); ok {
			return typed
		}
		panic(fmt.Sprintf("via: room %s exists with a different type", roomID))
	}

	r := &Room[T]{data: initial, roomID: roomID}
	rs.rooms[roomID] = r
	rs.upsertSortedIDLocked(roomID)
	return r
}

func (rs *Rooms) upsertSortedIDLocked(id string) {
	for _, existing := range rs.sortedIds {
		if existing == id {
			return
		}
	}
	rs.sortedIds = append(rs.sortedIds, id)
	sort.Slice(rs.sortedIds, func(i, j int) bool {
		li := strings.ToLower(rs.sortedIds[i])
		lj := strings.ToLower(rs.sortedIds[j])
		if li == lj {
			return rs.sortedIds[i] < rs.sortedIds[j]
		}
		return li < lj
	})
}

// Visit the rooms in sorted order, calling visitFn for each room.
// The isActive parameter indicates if the roomID matches the activeRoom.
func (rs *Rooms) VisitRooms(activeRoom string, visitFn func(id string, syncCount int, isActive bool)) {
	rs.mu.RLock()
	type roomInfo struct {
		id string
		r  interface{ SyncCount() int }
	}
	infos := make([]roomInfo, 0, len(rs.sortedIds))
	for _, id := range rs.sortedIds {
		if room, ok := rs.rooms[id]; ok {
			if r, ok := room.(interface{ SyncCount() int }); ok {
				infos = append(infos, roomInfo{id: id, r: r})
			}
		}
	}
	rs.mu.RUnlock()

	for _, info := range infos {
		visitFn(info.id, info.r.SyncCount(), info.id == activeRoom)
	}
}
