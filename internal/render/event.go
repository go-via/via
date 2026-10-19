package render

import "regexp"

// The event-key grammar lives here so package on, which builds keys, and via,
// which splices them into an attribute name raw, cannot drift apart. A name
// admits no quote or space, which would break out of the attribute, and no
// "__", which Datastar reads as the start of a modifier.
const (
	eventNamePattern = `[a-z][a-z0-9]*(?:[:.-][a-z0-9]+)*`
	// One segment of Datastar's "__"-split modifier list, which it splits again
	// on '.' into a name and tags.
	modifierPattern = `[a-z]+(?:\.[a-z0-9]+)*`
)

var (
	eventName = regexp.MustCompile(`^` + eventNamePattern + `$`)
	modifier  = regexp.MustCompile(`^` + modifierPattern + `$`)
	eventKey  = regexp.MustCompile(`^` + eventNamePattern + `(?:__` + modifierPattern + `)*$`)
)

// ValidEventName reports whether name is a bare event name, without modifiers.
func ValidEventName(name string) bool { return eventName.MatchString(name) }

// ValidModifier reports whether m is one modifier segment, such as
// "delay.300ms".
func ValidModifier(m string) bool { return modifier.MatchString(m) }

// ValidEventKey reports whether key is an event name followed by zero or more
// "__"-joined modifiers: the data-on suffix package on builds.
func ValidEventKey(key string) bool { return eventKey.MatchString(key) }
