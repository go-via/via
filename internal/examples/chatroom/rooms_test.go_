package main

import (
	"fmt"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestBroadcastOnDirtyOnly(t *testing.T) {
	rooms := NewRooms()
	room := GetRoom(rooms, "test", 0, true)
	called := make(chan bool, 1)

	room.RegisterWithCleanup(func() {
		called <- true
	})

	select {
	case <-called:
		t.Fatal("sync called without update")
	case <-time.After(600 * time.Millisecond):
	}

	room.Write(func(d *int) {
		*d = 42
	})

	select {
	case <-called:
	case <-time.After(1 * time.Second):
		t.Fatal("sync not called after update")
	}
}

func TestMultipleSubscribers(t *testing.T) {
	rooms := NewRooms()
	room := GetRoom(rooms, "test", "hello", true)
	var wg sync.WaitGroup
	wg.Add(2)

	room.RegisterWithCleanup(func() { wg.Done() })
	room.RegisterWithCleanup(func() { wg.Done() })

	room.Write(func(d *string) {
		*d = "world"
	})

	done := make(chan bool)
	go func() {
		wg.Wait()
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(1 * time.Second):
		t.Fatal("not all subscribers called")
	}
}

func TestGetRoomReuseAndTypeSafety(t *testing.T) {
	rooms := NewRooms()

	room1 := GetRoom(rooms, "test", 42, true)
	room2 := GetRoom(rooms, "test", 100, true)

	assert.Equal(t, room1, room2, "should return same room instance")

	assert.Panics(t, func() {
		GetRoom[string](rooms, "test", "panic", true)
	}, "should panic on type mismatch")
}

func TestReadWriteReflectsLatest(t *testing.T) {
	room := &Room[int]{data: 1}

	room.Read(func(d *int) {
		assert.Equal(t, 1, *d)
	})

	room.Write(func(d *int) {
		*d = 2
	})

	room.Read(func(d *int) {
		assert.Equal(t, 2, *d)
	})
}

func TestGetData(t *testing.T) {
	room := &Room[Chat]{data: Chat{
		Entries: []ChatEntry{
			{User: UserInfo{Name: "Alice", emoji: "🐼"}, Message: "Hello"},
		},
	}}

	chat := room.GetData()
	assert.Len(t, chat.Entries, 1)
	assert.Equal(t, "Alice", chat.Entries[0].User.Name)
	assert.Equal(t, "Hello", chat.Entries[0].Message)

	room.Write(func(c *Chat) {
		c.Entries = append(c.Entries, ChatEntry{
			User:    UserInfo{Name: "Bob", emoji: "🦊"},
			Message: "Hi",
		})
	})

	chat2 := room.GetData()
	assert.Len(t, chat2.Entries, 2)
	assert.Equal(t, "Bob", chat2.Entries[1].User.Name)
}

func TestRoomsConcurrentAccess(t *testing.T) {
	rooms := NewRooms()
	var wg sync.WaitGroup

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			room := GetRoom(rooms, "concurrent", 0, true)
			room.Write(func(d *int) {
				*d += n
			})
		}(i)
	}

	wg.Wait()
	room := GetRoom(rooms, "concurrent", 0, true)
	room.Read(func(d *int) {
		assert.NotEqual(t, 0, *d, "data should be updated")
	})
}

func TestSortedIDs_NewInsertionsAreSortedCaseInsensitive(t *testing.T) {
	rooms := NewRooms()

	GetRoom(rooms, "Python", 0, true)
	GetRoom(rooms, "go", 0, true)
	GetRoom(rooms, "Java", 0, true)
	GetRoom(rooms, "dotnet", 0, true)
	GetRoom(rooms, "Rust", 0, true)

	rooms.mu.RLock()
	ids := rooms.sortedIds
	rooms.mu.RUnlock()

	expected := []string{"dotnet", "go", "Java", "Python", "Rust"}
	assert.Equal(t, expected, ids, "rooms should be sorted case-insensitively")
}

func TestSortedIDs_DuplicateGetRoomDoesNotDuplicate(t *testing.T) {
	rooms := NewRooms()

	GetRoom(rooms, "Go", 0, true)
	GetRoom(rooms, "Go", 0, true)
	GetRoom(rooms, "Go", 0, true)

	rooms.mu.RLock()
	ids := rooms.sortedIds
	rooms.mu.RUnlock()

	assert.Len(t, ids, 1, "duplicate GetRoom should not create duplicate IDs")
	assert.Equal(t, "Go", ids[0])
}

func TestVisitRooms(t *testing.T) {
	rooms := NewRooms()

	room1 := GetRoom(rooms, "Go", 0, true)
	GetRoom(rooms, "Python", 0, true)
	GetRoom(rooms, "Rust", 0, true)

	room1.RegisterWithCleanup(func() {})
	room1.RegisterWithCleanup(func() {})

	visited := make(map[string]struct {
		syncCount int
		isActive  bool
	})

	rooms.VisitRooms("Go", func(id string, syncCount int, isActive bool) {
		visited[id] = struct {
			syncCount int
			isActive  bool
		}{syncCount, isActive}
	})

	assert.Len(t, visited, 3, "should visit all rooms")
	assert.Equal(t, 2, visited["Go"].syncCount, "Go room should have 2 subscribers")
	assert.True(t, visited["Go"].isActive, "Go should be marked active")
	assert.False(t, visited["Python"].isActive, "Python should not be active")
	assert.False(t, visited["Rust"].isActive, "Rust should not be active")
}

func TestVisitRooms_NoDeadlockWithConcurrentGetRoom(t *testing.T) {
	rooms := NewRooms()
	for i := 0; i < 5; i++ {
		GetRoom(rooms, fmt.Sprintf("room%d", i), 0, true)
	}

	done := make(chan bool)
	go func() {
		for i := 0; i < 100; i++ {
			rooms.VisitRooms("room0", func(id string, syncCount int, isActive bool) {
				time.Sleep(1 * time.Millisecond)
			})
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			GetRoom(rooms, fmt.Sprintf("room%d", i%10), 0, true)
			time.Sleep(1 * time.Millisecond)
		}
		done <- true
	}()

	select {
	case <-done:
		<-done
	case <-time.After(2 * time.Second):
		t.Fatal("deadlock detected: concurrent VisitRooms and GetRoom timed out")
	}
}

func TestHighConcurrencyTabSwitching(t *testing.T) {
	rooms := NewRooms()
	roomNames := []string{"Go", "Python", "Rust", "Java", "JS", "Kotlin"}
	for _, name := range roomNames {
		GetRoom(rooms, name, 0, true)
	}

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				activeRoom := roomNames[j%len(roomNames)]
				rooms.VisitRooms(activeRoom, func(id string, syncCount int, isActive bool) {})
				GetRoom(rooms, activeRoom, 0, false)
			}
		}(i)
	}

	done := make(chan bool)
	go func() {
		wg.Wait()
		done <- true
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("deadlock detected: simulating 4 tabs rapidly switching rooms timed out")
	}
}

func BenchmarkHighConcurrencyChat(b *testing.B) {
	rooms := NewRooms()
	room := GetRoom(rooms, "benchmark-room", Chat{}, true)
	numUsers := 10

	// Simulate numUsers subscribers to the room to make broadcasts do work.
	for i := 0; i < numUsers; i++ {
		room.RegisterWithCleanup(func() {
			chatData := room.GetData()
			view := renderChat(chatData)
			_ = view.Render(io.Discard)
		})
	}

	b.ResetTimer()
	b.SetParallelism(numUsers) // Simulate 10 concurrent users.

	// RunParallel will create goroutines and distribute b.N iterations among them.
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			room.Write(func(chat *Chat) {
				chat.Entries = append(chat.Entries, ChatEntry{
					User:    UserInfo{Name: "user", emoji: "🤖"},
					Message: "hello",
				})
			})
		}
	})
}
