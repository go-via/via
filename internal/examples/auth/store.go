package main

import (
	"errors"
	"sync"

	"golang.org/x/crypto/bcrypt"
)

type User struct {
	Name  string
	Email string
}

type Prefs struct {
	DarkMode string // "system", "light", "dark"
	Theme    string
}

type regFlash bool

var (
	users          = map[string]User{}
	passwordHashes = map[string][]byte{}
	prefs          = map[string]Prefs{}
	storeMu        sync.RWMutex
)

var (
	errAllFieldsRequired  = errors.New("all fields are required")
	errEmailAlreadyExists = errors.New("email already registered")
	errInvalidCredentials = errors.New("invalid email or password")
)

func register(name, email, password string) error {
	if name == "" || email == "" || password == "" {
		return errAllFieldsRequired
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	storeMu.Lock()
	defer storeMu.Unlock()
	if _, exists := passwordHashes[email]; exists {
		return errEmailAlreadyExists
	}
	users[email] = User{Name: name, Email: email}
	passwordHashes[email] = hash
	return nil
}

func authenticate(email, password string) (User, error) {
	storeMu.RLock()
	hash, exists := passwordHashes[email]
	user := users[email]
	storeMu.RUnlock()
	if !exists {
		return User{}, errInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword(hash, []byte(password)); err != nil {
		return User{}, errInvalidCredentials
	}
	return user, nil
}

func getPrefs(email string) (Prefs, bool) {
	storeMu.RLock()
	defer storeMu.RUnlock()
	p, ok := prefs[email]
	return p, ok
}

func setPrefs(email string, p Prefs) {
	storeMu.Lock()
	defer storeMu.Unlock()
	prefs[email] = p
}
