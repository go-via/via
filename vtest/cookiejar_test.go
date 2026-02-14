package vtest

import (
	"net/http"
	"testing"
)

func TestCookieJar_MergeCookies(t *testing.T) {
	jar := newCookieJar()

	// First request sets multiple cookies
	cookies1 := []*http.Cookie{
		{Name: "session", Value: "abc"},
		{Name: "theme", Value: "dark"},
	}
	jar.SetCookies("http://localhost", cookies1)

	// Second request sets different session but should keep theme
	cookies2 := []*http.Cookie{
		{Name: "session", Value: "xyz"},
		{Name: "lang", Value: "en"},
	}
	jar.SetCookies("http://localhost", cookies2)

	// Should have all 3 cookies: session, theme, lang (merge, not overwrite)
	result := jar.GetCookies("http://localhost")

	if len(result) != 3 {
		t.Fatalf("expected 3 cookies after merge, got %d: %v", len(result), result)
	}

	// Verify session cookie has latest value
	found := false
	for _, c := range result {
		if c.Name == "session" && c.Value == "xyz" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected session cookie with value 'xyz'")
	}
}
