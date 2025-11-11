package main

import (
	"fmt"
	"math/rand"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

var (
	WithSignal    = via.WithSignal
	WithSignalInt = via.WithSignalInt
)

func main() {
	rooms := NewRooms()
	v := via.New()
	v.Config(via.Options{
		DocumentTitle: "ViaChat",
		LogLvl:        via.LogLevelDebug,
	})

	v.AppendToHead(
		h.Link(h.Rel("stylesheet"), h.Href("https://cdn.jsdelivr.net/npm/@picocss/pico@2/css/pico.min.css")),
		h.StyleEl(h.Raw(`
				article { margin-bottom: 0.5rem; padding: 0.75rem; }
				.chat-message { display: flex; gap: 0.75rem; }
				.chat-message.right { flex-direction: row-reverse; }
				.avatar { 
					width: 2rem; 
					height: 2rem; 
					border-radius: 50%; 
					background: var(--pico-muted-border-color);
					display: grid;
					place-items: center;
					font-size: 1.5rem;
					user-select: none;
				}
				.bubble { flex: 1; }
				.bubble p { margin: 0; }
				.chat-history { max-height: 60vh; overflow-y: auto; scroll-behavior: smooth; }
			`)),
	)

	availableRooms := []string{"Clojure", "Dotnet", "Go", "Java", "JS", "Kotlin", "Python", "Rust"}
	for _, room := range availableRooms {
		GetRoom(rooms, room, Chat{}, true)
	}

	v.Page("/", func(c *via.Context) {
		roomName := c.Signal("Go")
		currentUser := NewUserInfo(randAnimal())
		statement := c.Signal("")

		var currentRoom *Room[Chat]
		var unregisterFromRoom func()

		switchRoom := func() {
			if unregisterFromRoom != nil {
				unregisterFromRoom()
			}
			currentRoom = GetRoom(rooms, roomName.String(), Chat{}, false)
			rooms.DirtyAll() // Because counts are showing, tabs need updating.
			unregisterFromRoom = currentRoom.RegisterWithCleanup(c.Sync)
			c.Sync()
		}

		switchRoomAction := c.Action(func() {
			fmt.Println("Switch to", roomName.String())
			switchRoom()
		})

		// fmt.Println("calling switchRoom")
		switchRoom()

		say := c.Action(func() {
			fmt.Println("Saying", statement.String())
			if statement.String() != "" && currentRoom != nil {
				currentRoom.Write(func(chat *Chat) {
					chat.Entries = append(chat.Entries, ChatEntry{
						User:    currentUser,
						Message: statement.String(),
					})
				})
				statement.SetValue("")
				// Update the UI right away so feels snappy.
				c.Sync()
			}
		})

		c.View(func() h.H {
			var tabs []h.H
			rooms.VisitRooms(roomName.String(), func(roomID string, members int, isActive bool) {
				displayName := roomID
				if members > 0 {
					displayName = fmt.Sprintf("%s (%d)", roomID, members)
				}
				if isActive {
					tabs = append(tabs, h.Li(
						h.A(
							h.Href(""),
							h.Attr("aria-current", "page"),
							h.Text(displayName),
							switchRoomAction.OnClick(WithSignal(roomName, roomID)),
						),
					))
				} else {
					tabs = append(tabs, h.Li(
						h.A(
							h.Href("#"),
							h.Text(displayName),
							switchRoomAction.OnClick(via.WithSignal(roomName, roomID)),
						),
					))
				}
			})

			var messages []h.H
			if currentRoom != nil {
				currentRoom.Read(func(chat *Chat) {
					for _, entry := range chat.Entries {
						isCurrentUser := entry.User == currentUser
						alignment := ""
						if isCurrentUser {
							alignment = "right"
						}

						messageChildren := []h.H{h.Class("chat-message " + alignment)}
						if !isCurrentUser {
							messageChildren = append(messageChildren, entry.User.Avatar())
						}
						messageChildren = append(messageChildren,
							h.Div(h.Class("bubble"),
								h.P(h.Text(entry.Message)),
							),
						)

						messages = append(messages, h.Div(messageChildren...))
					}
				})
			}

			chatHistory := []h.H{h.Class("chat-history")}
			chatHistory = append(chatHistory, messages...)

			return h.Main(h.Class("container"),
				h.Nav(
					h.Attr("role", "tab-control"),
					h.Ul(tabs...),
				),
				h.Div(chatHistory...),
				h.Div(
					h.Style("display: flex; align-items: center; gap: 0.75rem;"),
					currentUser.Avatar(),
					h.FieldSet(
						h.Attr("role", "group"),
						h.Input(
							h.Type("text"),
							h.Placeholder(fmt.Sprintf("%s says...", currentUser.Name)),
							statement.Bind(),
							h.Attr("autofocus"),
							say.OnKeyDown("Enter"),
						),
						h.Button(h.Text("Say"), say.OnClick()),
					),
				),
			)
		})
	})

	v.Start()
}

type UserInfo struct {
	Name  string
	emoji string
}

func NewUserInfo(name, emoji string) UserInfo {
	return UserInfo{Name: name, emoji: emoji}
}

func (u *UserInfo) Avatar() h.H {
	return h.Div(h.Class("avatar"), h.Attr("title", u.Name), h.Text(u.emoji))
}

type ChatEntry struct {
	User    UserInfo
	Message string
}

type Chat struct {
	Entries []ChatEntry
}

func randAnimal() (string, string) {
	adjectives := []string{"Happy", "Clever", "Brave", "Swift", "Gentle", "Wise", "Bold", "Calm", "Eager", "Fierce"}

	animals := []string{"Panda", "Tiger", "Eagle", "Dolphin", "Fox", "Wolf", "Bear", "Hawk", "Otter", "Lion"}
	whichAnimal := rand.Intn(len(animals))

	emojis := []string{"🐼", "🐯", "🦅", "🐬", "🦊", "🐺", "🐻", "🦅", "🦦", "🦁"}
	return adjectives[rand.Intn(len(adjectives))] + " " + animals[whichAnimal], emojis[whichAnimal]
}
