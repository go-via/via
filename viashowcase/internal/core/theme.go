package core

// Themes lists the 19 picocss color theme names (mirrors picocss.AllPicoThemes).
var Themes = []string{
	"amber", "blue", "cyan", "fuchsia", "green",
	"grey", "indigo", "jade", "lime", "orange",
	"pink", "pumpkin", "purple", "red", "sand",
	"slate", "violet", "yellow", "zinc",
}

const defaultTheme = "amber"

func ValidTheme(s string) bool {
	for _, t := range Themes {
		if t == s {
			return true
		}
	}
	return false
}

// ResolveTheme returns s if it is a valid theme, else the brand default "amber".
func ResolveTheme(s string) string {
	if ValidTheme(s) {
		return s
	}
	return defaultTheme
}

// ValidMode normalizes a dark-mode preference: "system"|"dark"|"light",
// defaulting to "dark".
func ValidMode(s string) string {
	switch s {
	case "system", "dark", "light":
		return s
	default:
		return "dark"
	}
}
