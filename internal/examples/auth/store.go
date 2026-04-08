package main

import (
	"errors"
	"sync"

	"github.com/go-via/via"
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
	users     = map[string]User{}
	passwords = map[string]string{}
	prefs     = map[string]Prefs{}
	storeMu   sync.RWMutex
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
	storeMu.Lock()
	defer storeMu.Unlock()
	if _, exists := passwords[email]; exists {
		return errEmailAlreadyExists
	}
	users[email] = User{Name: name, Email: email}
	passwords[email] = password
	return nil
}

func authenticate(email, password string) (User, error) {
	storeMu.RLock()
	defer storeMu.RUnlock()
	stored, exists := passwords[email]
	if !exists || stored != password {
		return User{}, errInvalidCredentials
	}
	return users[email], nil
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

func applyDarkMode(ctx *via.Ctx, mode string) {
	switch mode {
	case "light":
		ctx.MarshalAndPatchSignals(map[string]any{"_picoDarkMode": false})
	case "dark":
		ctx.MarshalAndPatchSignals(map[string]any{"_picoDarkMode": true})
	}
	// "system": no-op — browser default applies on next page load
}
