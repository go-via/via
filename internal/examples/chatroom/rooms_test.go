package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

type Statement struct {
	text   string
	author TestUserInfo
}

type RoomData struct {
	convo []Statement
}

type TestUserInfo struct {
	Name string
}

func TestRoomsZero(t *testing.T) {
	rooms := NewRooms[RoomData, TestUserInfo]()
	assert.NotNil(t, rooms)
}

func TestRoomsMany(t *testing.T) {
	names := []string{"a", "b"}
	rooms := NewRooms[RoomData, TestUserInfo](names...)
	assert.NotNil(t, rooms)
	assert.Equal(t, 2, len(rooms.names))

	// Visit
	seen := 0
	rooms.Visit(func(name string) { seen++ })
	assert.Equal(t, seen, 2)

	// GetRoom fail
	_, ok := rooms.Get("z")
	assert.False(t, ok)
	// GetRoom
	rm, ok := rooms.Get("a")
	assert.True(t, ok)
	assert.NotNil(t, rm)
	assert.Equal(t, string("a"), rm.Name)
}

type DummySyncable struct {
	room        *Room[RoomData, TestUserInfo]
	timesCalled int
}

func (ds *DummySyncable) Sync() {
	// Data() hits deadlock conditions from Publish()
	ds.room.GetData()
	ds.timesCalled++
}

func (ds *DummySyncable) Connected() bool {
	return true
}

func TestRoomJoinLeaveChannels(t *testing.T) {
	rooms := NewRooms[RoomData, TestUserInfo](string("a"))
	rm, _ := rooms.Get("a")
	u1 := TestUserInfo{"Bob"}
	u1Context := DummySyncable{room: rm}

	rooms.Start()
	defer rooms.Stop()
	uas := UserAndSync[RoomData, TestUserInfo]{user: &u1, sync: &u1Context}

	// Joining a room does *not* mark it dirty. It's on the user to call Sync() -
	// so the user gets the update immediately.
	rm.Join(&uas)

	// // Give it time to process
	time.Sleep(1 * time.Millisecond)

	assert.Equal(t, rm.dirty, false)
	assert.Equal(t, len(rm.members), 1)

	// Room Data
	rm.UpdateData(func(data *RoomData) {
		data.convo = append(data.convo, Statement{"Hello", u1})
	})
	assert.Equal(t, rm.dirty, true)

	data := rm.GetData()
	assert.Equal(t, len(data.convo), 1)

	// BROADCAST to connected users. Clears the dirty flag.
	rm.Publish()
	time.Sleep(1 * time.Millisecond)
	assert.Equal(t, rm.dirty, false)
	assert.Equal(t, u1Context.timesCalled, 1)

	// Leave
	rm.Leave(&u1)
	time.Sleep(1 * time.Millisecond)

	assert.Equal(t, len(rm.members), 0)
}
