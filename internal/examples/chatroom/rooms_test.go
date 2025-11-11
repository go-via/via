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

func (u TestUserInfo) getUserId() string {
	return u.Name
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

func TestRoomJoinLeaveChannels(t *testing.T) {
	rooms := NewRooms[RoomData, TestUserInfo](string("a"))
	rm, _ := rooms.Get("a")
	u1 := TestUserInfo{"Bob"}

	rm.Start()
	defer rm.Stop()
	rm.Join(u1)

	// // Give it time to process
	time.Sleep(1 * time.Millisecond)

	assert.Equal(t, rm.MemberCount(), 1)

	// Leave
	rm.Leave(u1)
	time.Sleep(1 * time.Millisecond)

	assert.Equal(t, rm.MemberCount(), 0)
	assert.Equal(t, rm.Dirty(), false)

	// Room Data
	rm.UpdateData(func(data *RoomData) {
		data.convo = append(data.convo, Statement{"Hello", u1})
	})
	assert.Equal(t, rm.Dirty(), true)

	data := rm.GetData()
	assert.Equal(t, len(data.convo), 1)

	// Context
}
