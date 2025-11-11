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
	v.AppendToHead(
		h.Link(h.Rel("stylesheet"),
			h.Href("https://cdn.jsdelivr.net/npm/@picocss/pico@2/css/pico.min.css")),
		h.StyleEl(h.Raw(`
				:root {
					--chat-input-height: 88px;
				}
				html, body {
					height: 100%;
				}
				body {
					margin: 0;
				}
				.chat-app {
					display: flex;
					flex-direction: column;
					height: 100vh;
					height: 100dvh;
					overflow: hidden;
				}
				nav[role="tab-control"] ul li a[aria-current="page"] {
					background-color: var(--pico-primary-background);
					color: var(--pico-primary-inverse);
					border-bottom: 2px solid var(--pico-primary);
				}
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
				.chat-history {
					flex: 1 1 auto;
					overflow-y: auto;
					-webkit-overflow-scrolling: touch;
					padding-bottom: calc(var(--chat-input-height) + env(safe-area-inset-bottom));
				}
				.chat-input {
					position: fixed;
					left: 0;
					right: 0;
					bottom: 0;
					z-index: 100;
					background: var(--pico-background-color);
					display: flex;
					align-items: center;
					gap: 0.75rem;
					padding: 0.75rem 1rem;
					padding-bottom: calc(0.75rem + env(safe-area-inset-bottom));
					border-top: 1px solid var(--pico-muted-border-color);
					box-shadow: 0 -2px 8px rgba(0, 0, 0, 0.1);
				}
				.chat-input fieldset {
					flex: 1 1 auto;
					margin: 0;
				}
			`)),
		h.Script(h.Raw(`
				function scrollChatToBottom() {
					const chatHistory = document.querySelector('.chat-history');
					if (chatHistory) chatHistory.scrollTop = chatHistory.scrollHeight;
				}
				setInterval(scrollChatToBottom, 100);
			`)),
	)
	v.Config(via.Options{
		DocumentTitle: "ViaChat",
		LogLvl:        via.LogLevelDebug,
		Plugins:       []via.Plugin{LiveReloadPlugin},
	})

	availableRooms := []string{"Clojure", "Dotnet", "Go", "Java", "JS", "Kotlin", "Python", "Rust"}

	// Seed random bot messages in all rooms except "Go".
	botUser := NewUserInfo(randAnimal())
	botUser2 := NewUserInfo(randAnimal())
	for _, roomName := range availableRooms {
		room := GetRoom(rooms, roomName, Chat{}, true)
		if roomName != "Go" {
			var messages []ChatEntry
			for i := 0; i < 2; i++ {
				messages = append(messages, ChatEntry{
					User:    botUser,
					Message: deepThought(),
				})
				messages = append(messages, ChatEntry{
					User:    botUser2,
					Message: deepThought(),
				})
			}
			PopulateMessages(room, messages)
		}
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
			if currentRoom != nil {
				unregisterFromRoom = currentRoom.RegisterWithCleanup(c.Sync)
			}
		}

		switchRoomAction := c.Action(func() {
			switchRoom()
		})

		switchRoom()

		say := c.Action(func() {
			if currentRoom != nil {
				msg := statement.String()
				if msg == "" {
					// For testing, generate random stuff.
					msg = deepThought()
				} else {
					statement.SetValue("")
				}
				currentRoom.Write(func(chat *Chat) {
					chat.Entries = append(chat.Entries, ChatEntry{
						User:    currentUser,
						Message: msg,
					})
				})
				// c.Sync()
			}
		})

		c.View(func() h.H {
			var tabs []h.H
			rooms.VisitRooms(roomName.String(), func(roomID string, members int, isActive bool) {
				if isActive {
					tabs = append(tabs, h.Li(
						h.A(
							h.Href(""),
							h.Attr("aria-current", "page"),
							h.Text(roomID),
							switchRoomAction.OnClick(WithSignal(roomName, roomID)),
						),
					))
				} else {
					tabs = append(tabs, h.Li(
						h.A(
							h.Href("#"),
							h.Text(roomID),
							switchRoomAction.OnClick(via.WithSignal(roomName, roomID)),
						),
					))
				}
			})

			var chatHistoryView h.H
			if currentRoom != nil {
				chatHistoryView = renderChat(currentRoom.GetData())
			} else {
				chatHistoryView = h.Div(h.Class("chat-history"))
			}

			return h.Main(h.Class("container chat-app"),
				h.Nav(
					h.Attr("role", "tab-control"),
					h.Ul(tabs...),
				),
				chatHistoryView,
				h.Div(
					h.Class("chat-input"),
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

	v.Start(":3000")
}

func renderChat(chat Chat) h.H {
	var messages []h.H
	for _, entry := range chat.Entries {
		messageChildren := []h.H{h.Class("chat-message ")}
		messageChildren = append(messageChildren, entry.User.Avatar())
		messageChildren = append(messageChildren,
			h.Div(h.Class("bubble"),
				h.P(h.Text(entry.Message)),
			),
		)
		messages = append(messages, h.Div(messageChildren...))
	}
	chatHistory := []h.H{h.Class("chat-history")}
	chatHistory = append(chatHistory, messages...)
	return h.Div(chatHistory...)
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

// PopulateMessages adds multiple chat entries to a Chat room at once.
func PopulateMessages(r *Room[Chat], entries []ChatEntry) {
	r.Write(func(chat *Chat) {
		chat.Entries = append(chat.Entries, entries...)
	})
}

func randAnimal() (string, string) {
	adjectives := []string{"Happy", "Clever", "Brave", "Swift", "Gentle", "Wise", "Bold", "Calm", "Eager", "Fierce"}

	animals := []string{"Panda", "Tiger", "Eagle", "Dolphin", "Fox", "Wolf", "Bear", "Hawk", "Otter", "Lion"}
	whichAnimal := rand.Intn(len(animals))

	emojis := []string{"🐼", "🐯", "🦅", "🐬", "🦊", "🐺", "🐻", "🦅", "🦦", "🦁"}
	return adjectives[rand.Intn(len(adjectives))] + " " + animals[whichAnimal], emojis[whichAnimal]
}

var thoughtIdx = -1

func deepThought() string {
	sentences := []string{"I like turtles.", "How do you clean up signals?", "Just use Lisp.", "You're complecting things.",
		"The internet is a series of tubes.", "Go is not a good language.", "I love Python.", "JavaScript is everywhere.", "Kotlin is great for Android.",
		"Rust is memory safe.", "Dotnet is cross platform.", "Rewrite it in Rust", "Is it web scale?", "PRs welcome.", "Have you tried turning it off and on again?",
		"Clojure has macros.", "Functional programming is the future.", "OOP is dead.", "Tabs are better than spaces.", "Spaces are better than tabs.",
		"I use Emacs.", "Vim is the best editor.", "VSCode is bloated.", "I code in the browser.", "Serverless is the way to go.", "Containers are lightweight VMs.",
		"Microservices are the future.", "Monoliths are easier to manage.", "Agile is just Scrum.", "Waterfall still has its place.", "DevOps is a culture.", "CI/CD is essential.",
		"Testing is important.", "TDD saves time.", "BDD improves communication.", "Documentation is key.", "APIs should be RESTful.", "GraphQL is flexible.", "gRPC is efficient.",
		"WebAssembly is the future of web apps.", "Progressive Web Apps are great.", "Single Page Applications can be overkill.", "Jamstack is modern web development.",
		"CDNs improve performance.", "Edge computing reduces latency.", "5G will change everything.", "AI will take over coding.", "Machine learning is powerful.",
		"Data science is in demand.", "Big data requires big storage.", "Cloud computing is ubiquitous.", "Hybrid cloud offers flexibility.", "Multi-cloud avoids vendor lock-in.",
		"That can't possibly work", "First!", "Leeroy Jenkins!", "I love open source.", "Closed source has its place.", "Licensing is complicated.",
	}
	thoughtIdx = (thoughtIdx + 1) % len(sentences)
	return sentences[thoughtIdx]

}
