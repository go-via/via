package main

import (
	"math/rand"

	"github.com/go-via/via"
	"github.com/go-via/via/h"
)

func main() {

	v := via.New()
	LiveReloadPlugin(v)
	v.Config(via.Options{
		DocumentTitle: "ViaChat",
		DocumentHeadIncludes: []h.H{
			h.Link(h.Rel("stylesheet"), h.Href("https://cdn.jsdelivr.net/npm/@picocss/pico@2/css/pico.min.css")),
			h.StyleEl(h.Raw(`
				article { margin-bottom: 0.5rem; padding: 0.75rem; }
				.chat-message { display: flex; gap: 0.75rem; }
				.chat-message.right { flex-direction: row-reverse; }
				.avatar { 
					width: 2rem; 
					height: 2rem; 
					border-radius: 50%; 
					background: #e0e0e0;
					display: grid;
					place-items: center;
					font-size: 1.5rem;
					user-select: none;
+					position: relative;
					top: -8px;
				}
				.bubble { flex: 1; }
				.bubble p { margin: 0; }
				.chat-history { max-height: 60vh; overflow-y: auto; scroll-behavior: smooth; }
				.container { max-width: 800px; }
			`)),
			h.Script(h.Raw(`
				function scrollChatToBottom() {
					const chatHistory = document.querySelector('.chat-history');
					chatHistory.scrollTop = chatHistory.scrollHeight;
				}
				setInterval(scrollChatToBottom, 750);
			`)),
			liveReloadScript(),
		},
	})

	v.Page("/", func(c *via.Context) {

		currentUser := NewUserInfo(randAnimal())
		statement := c.Signal("")
		say := c.Action(func() {
			if statement.String() != "" {
				chat.Entries = append(chat.Entries, ChatEntry{
					User:    currentUser,
					Message: statement.String(),
				})
				statement.SetValue("")
				v.BroadcastSync()
			}
		})

		c.View(func() h.H {

			var messages []h.H
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

				messages = append(messages,
					h.Article(
						h.Div(messageChildren...),
					),
				)
			}

			chatHistory := []h.H{h.Class("chat-history")}
			chatHistory = append(chatHistory, messages...)

			return h.Div(h.Class("container"),
				h.H1(h.Text("Chat")),
				h.Div(chatHistory...),
				h.Div(
					h.Style("display: flex; align-items: center; gap: 0.75rem;"),
					currentUser.Avatar(),
					h.FieldSet(
						h.Attr("role", "group"),
						h.Input(
							h.Type("text"),
							h.Placeholder("Type a message..."),
							statement.Bind(),
							h.Attr("autofocus"),
							say.OnEnterKey(),
						),
						h.Button(h.Text("Say"), say.OnClick()),
					),
				),
			)
		})
	})

	v.Start(":3000")
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

var chat Chat

func randAnimal() (string, string) {
	adjectives := []string{"Happy", "Clever", "Brave", "Swift", "Gentle", "Wise", "Bold", "Calm", "Eager", "Fierce"}

	animals := []string{"Panda", "Tiger", "Eagle", "Dolphin", "Fox", "Wolf", "Bear", "Hawk", "Otter", "Lion"}
	whichAnimal := rand.Intn(len(animals))

	emojis := []string{"🐼", "🐯", "🦅", "🐬", "🦊", "🐺", "🐻", "🦅", "🦦", "🦁"}
	return adjectives[rand.Intn(len(adjectives))] + " " + animals[whichAnimal], emojis[whichAnimal]
}
