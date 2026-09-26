package main

import (
	"errors"
	"sync"
)

// User is the session-stored identity. Plain app data.
type User struct {
	ID          int
	Email, Name string
	Avatar      string // the /avatar/{id} path, set on upload
}

type Thread struct {
	ID            int
	Title, Author string
}

type Post struct{ Author, Body string }

type avatar struct {
	typ  string
	data []byte
}

// Store is the whole app's mutable state behind one mutex. Stdlib only; no
// database, no framework types.
type Store struct {
	mu      sync.Mutex
	byEmail map[string]*User
	byID    map[int]*User
	threads []Thread
	posts   map[int][]Post
	avatars map[int]avatar
	seqU    int
	seqT    int
}

func newStore() *Store {
	return &Store{byEmail: map[string]*User{}, byID: map[int]*User{}, posts: map[int][]Post{}, avatars: map[int]avatar{}}
}

// createUser registers an identity by email alone. via ships with no
// dependencies, and a correct password store needs a slow KDF (bcrypt, argon2)
// that the stdlib does not provide — so this example deliberately implements no
// password storage at all rather than demonstrate a bad one.
func (s *Store) createUser(email, name string) (User, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if email == "" {
		return User{}, errors.New("an email is required")
	}
	if _, ok := s.byEmail[email]; ok {
		return User{}, errors.New("that email is already registered")
	}
	if name == "" {
		name = email
	}
	s.seqU++
	u := &User{ID: s.seqU, Email: email, Name: name}
	s.byEmail[email] = u
	s.byID[u.ID] = u
	return *u, nil
}

func (s *Store) lookup(email string) (User, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	u, ok := s.byEmail[email]
	if !ok {
		return User{}, false
	}
	return *u, true
}

func (s *Store) save(u User) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.byID[u.ID]; ok {
		*rec = u
	}
}

func (s *Store) allThreads() []Thread {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]Thread{}, s.threads...)
}

func (s *Store) newThread(author, title string) {
	if title == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.seqT++
	s.threads = append(s.threads, Thread{ID: s.seqT, Title: title, Author: author})
}

func (s *Store) thread(id int) (title string, posts []Post, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, t := range s.threads {
		if t.ID == id {
			return t.Title, append([]Post{}, s.posts[id]...), true
		}
	}
	return "", nil, false
}

func (s *Store) reply(id int, author, body string) {
	if body == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.posts[id] = append(s.posts[id], Post{Author: author, Body: body})
}

func (s *Store) setAvatar(id int, typ string, data []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.avatars[id] = avatar{typ: typ, data: data}
}

func (s *Store) avatar(id int) (typ string, data []byte, ok bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	a, ok := s.avatars[id]
	return a.typ, a.data, ok
}
