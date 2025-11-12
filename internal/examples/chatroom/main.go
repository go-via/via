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

// To drive heavy traffic: start several browsers and put this in the console:
// setInterval(() => document.querySelector('input').dispatchEvent(new KeyboardEvent('keydown', {key: 'Enter', code: 'Enter', keyCode: 13, bubbles: true})), 500);
// Or, as a bookmarklet:
// javascript:(function(){setInterval(()=>{const input=document.querySelector('input');if(input){input.dispatchEvent(new KeyboardEvent('keydown',{key:'Enter',code:'Enter',keyCode:13,bubbles:true}))}},500)})();

func main() {
	v := via.New()
	v.Config(via.Options{
		DevMode:       true,
		DocumentTitle: "ViaChat",
		LogLvl:        via.LogLevelInfo,
	})

	v.AppendToHead(
		h.Link(h.Rel("stylesheet"), h.Href("https://cdn.jsdelivr.net/npm/@picocss/pico@2/css/pico.min.css")),
		h.StyleEl(h.Raw(`
				body { margin: 0; }
				main { 
					display: flex;
					flex-direction: column;
					height: 100vh;
				}
				nav[role="tab-control"] ul li a[aria-current="page"] {
					background-color: var(--pico-primary-background);
					color: var(--pico-primary-inverse);
					border-bottom: 2px solid var(--pico-primary);
				}
				.chat-message { display: flex; gap: 0.75rem; }
				.avatar { 
					width: 2rem; 
					height: 2rem; 
					border-radius: 50%; 
					background: var(--pico-muted-border-color);
					display: grid;
					place-items: center;
					font-size: 1.5rem;
				}
				.bubble { flex: 1; }
				.bubble p { margin: 0; }
				.chat-history {
					flex: 1;
					overflow-y: auto;
					padding-bottom: calc(88px + env(safe-area-inset-bottom));
					scrollbar-width: none;
				}
				.chat-history::-webkit-scrollbar {
					display: none;
				}
				.chat-input {
					position: fixed;
					left: 0;
					right: 0;
					bottom: 0;
					background: var(--pico-background-color);
					display: flex;
					align-items: center;
					gap: 0.75rem;
					padding: 0.75rem 1rem calc(0.75rem + env(safe-area-inset-bottom));
					border-top: 1px solid var(--pico-muted-border-color);
				}
				.chat-input fieldset {
					flex: 1;
					margin: 0;
				}
			`)), h.Script(h.Raw(`
				function scrollChatToBottom() {
					const chatHistory = document.querySelector('.chat-history');
					if (chatHistory) chatHistory.scrollTop = chatHistory.scrollHeight;
				}
				setInterval(scrollChatToBottom, 100);
			`)),
	)
	rooms := NewRooms[Chat, UserInfo]("Clojure", "Dotnet", "Go", "Java", "JS", "Kotlin", "Python", "Rust")
	rooms.Start()

	v.Page("/", func(c *via.Context) {
		roomName := c.Signal("Go")

		// Need to be careful about reading signals: can cause race conditions.
		// So use a string as much as possible.
		var roomNameString string
		currentUser := NewUserInfo(randAnimal())
		statement := c.Signal("")

		var currentRoom *Room[Chat, UserInfo]

		switchRoom := func() {
			newRoom, _ := rooms.Get(string(roomName.String()))
			fmt.Println(">> switchRoom to ", newRoom.Name)
			if currentRoom != nil && currentRoom != newRoom {
				fmt.Println("LEAVING old room")
				currentRoom.Leave(&currentUser)
			}
			newRoom.Join(&UserAndSync[Chat, UserInfo]{user: &currentUser, sync: c})
			currentRoom = newRoom
			roomNameString = newRoom.Name
			c.Sync()
		}

		switchRoomAction := c.Action(func() {
			switchRoom()
		})

		switchRoom()

		say := c.Action(func() {
			msg := statement.String()
			if msg == "" {
				// For testing, generate random stuff.
				msg = thingsDevsSay()
			} else {
				statement.SetValue("")
			}
			if currentRoom != nil {
				currentRoom.UpdateData(func(chat *Chat) {
					chat.Entries = append(chat.Entries, ChatEntry{
						User:    currentUser,
						Message: msg,
					})
				})
				statement.SetValue("")
				// Update the UI right away so feels snappy.
				c.Sync()
			}
		})

		c.View(func() h.H {
			var tabs []h.H
			rooms.Visit(func(n string) {
				if n == roomNameString {
					tabs = append(tabs, h.Li(
						h.A(
							h.Href(""),
							h.Attr("aria-current", "page"),
							h.Text(n),
							switchRoomAction.OnClick(WithSignal(roomName, n)),
						),
					))
				} else {
					tabs = append(tabs, h.Li(
						h.A(
							h.Href("#"),
							h.Text(n),
							switchRoomAction.OnClick(via.WithSignal(roomName, n)),
						),
					))
				}
			})

			var messages []h.H
			if currentRoom != nil {
				chat := currentRoom.GetData(func(c *Chat) Chat {
					n := len(c.Entries)
					start := n - 50
					if start < 0 {
						start = 0
					}
					trimmed := make([]ChatEntry, n-start)
					copy(trimmed, c.Entries[start:])
					out := *c
					out.Entries = trimmed
					return out
				})
				for _, entry := range chat.Entries {

					messageChildren := []h.H{h.Class("chat-message"), entry.User.Avatar()}
					messageChildren = append(messageChildren,
						h.Div(h.Class("bubble"),
							h.P(h.Text(entry.Message)),
						),
					)

					messages = append(messages, h.Div(messageChildren...))
				}
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

var thingIdx = rand.Intn(len(things)) - 1
var things = []string{"I like turtles.", "How do you clean up signals?", "Just use Lisp.", "You're complecting things.",
	"The internet is a series of tubes.", "Go is not a good language.", "I love Python.", "JavaScript is everywhere.", "Kotlin is great for Android.",
	"Rust is memory safe.", "Dotnet is cross platform.", "Rewrite it in Rust", "Is it web scale?", "PRs welcome.", "Have you tried turning it off and on again?",
	"Clojure has macros.", "Functional programming is the future.", "OOP is dead.", "Tabs are better than spaces.", "Spaces are better than tabs.",
	"I use Emacs.", "Vim is the best editor.", "VSCode is bloated.", "I code in the browser.", "Serverless is the way to go.", "Containers are lightweight VMs.",
	"Microservices are the future.", "Monoliths are easier to manage.", "Agile is just Scrum.", "Waterfall still has its place.", "DevOps is a culture.", "CI/CD is essential.",
	"Testing is important.", "TDD saves time.", "BDD improves communication.", "Documentation is key.", "APIs should be RESTful.", "GraphQL is flexible.", "gRPC is efficient.",
	"WebAssembly is the future of web apps.", "Progressive Web Apps are great.", "Single Page Applications can be overkill.", "Jamstack is modern web development.",
	"CDNs improve performance.", "Edge computing reduces latency.", "5G will change everything.", "AI will take over coding.", "Machine learning is powerful.",
	"Data science is in demand.", "Big data requires big storage.", "Cloud computing is ubiquitous.", "Hybrid cloud offers flexibility.", "Multi-cloud avoids vendor lock-in.",
	"That can't possibly work", "First!", "Leeroy Jenkins!", "I love open source.", "Closed source has its place.", "Licensing is complicated."}

func thingsDevsSay() string {

	thingIdx = (thingIdx + 1) % len(things)
	return things[thingIdx]

}
