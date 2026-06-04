package via

import (
	"fmt"
	"reflect"
	"strings"
)

type fieldRole int

const (
	roleNone fieldRole = iota
	roleSignal
	roleState
	roleStateSess
	roleStateApp
	roleStateAppEvents
	roleParam
	roleQuery
	roleFile
	roleChild
)

// walkStruct recursively flattens a composition tree into the descriptor.
// pathPrefix is the qualified wire key prefix for nested children
// (empty at root, "Chart" at one level, "Tab.Chart" at two).
// indexPath is the slice of struct-field indices from the root *C to the
// struct being walked (so the runtime can address nested fields via
// reflect.Value.FieldByIndex).
func walkStruct(d *cmpDescriptor, typ reflect.Type, indexPath []int, pathPrefix string) {
	for i := range typ.NumField() {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		// Allocate exactly once (len+1) instead of the double-append idiom,
		// which can over-allocate via Go's slice growth heuristics.
		fieldPath := make([]int, len(indexPath)+1)
		copy(fieldPath, indexPath)
		fieldPath[len(indexPath)] = i
		role := classifyField(f)
		if role != roleNone {
			validateViaTagOptions(typ, f)
		}
		switch role {
		case roleSignal, roleState:
			kind := kindSignal
			if role == roleState {
				kind = kindState
			}
			d.signalSlots = append(d.signalSlots, signalSlot{
				fieldPath: fieldPath,
				kind:      kind,
				wireKey:   qualify(pathPrefix, parseLocalID(f)),
				initRaw:   parseInitTag(f),
			})
		case roleStateSess, roleStateApp, roleStateAppEvents:
			kind := scopeValue
			if role == roleStateAppEvents {
				kind = scopeLog
			}
			d.scopeSlots = append(d.scopeSlots, scopeSlot{
				fieldPath: fieldPath,
				wireKey:   qualify(pathPrefix, parseLocalID(f)),
				kind:      kind,
			})
		case roleParam:
			d.paramSlots = append(d.paramSlots, kindedSlot{
				fieldPath: fieldPath,
				name:      f.Tag.Get("path"),
				kind:      f.Type.Kind(),
			})
		case roleQuery:
			d.querySlots = append(d.querySlots, kindedSlot{
				fieldPath: fieldPath,
				name:      f.Tag.Get("query"),
				kind:      f.Type.Kind(),
			})
		case roleFile:
			d.fileSlots = append(d.fileSlots, fileSlot{
				fieldPath: fieldPath,
				wireKey:   qualify(pathPrefix, parseLocalID(f)),
			})
		case roleChild:
			child := f.Type
			if child.Kind() == reflect.Pointer {
				child = child.Elem()
			}
			walkStruct(d, child, fieldPath, qualify(pathPrefix, f.Name))
		}
	}
}

func classifyField(f reflect.StructField) fieldRole {
	if _, ok := f.Tag.Lookup("path"); ok {
		return roleParam
	}
	if _, ok := f.Tag.Lookup("query"); ok {
		return roleQuery
	}
	if isSignalType(f.Type) {
		return roleSignal
	}
	if isStateType(f.Type) {
		return roleState
	}
	if isStateSessType(f.Type) {
		return roleStateSess
	}
	if isStateAppType(f.Type) {
		return roleStateApp
	}
	if isStateAppEventsType(f.Type) {
		return roleStateAppEvents
	}
	if isFileType(f.Type) {
		return roleFile
	}
	if isChildComposition(f.Type) {
		return roleChild
	}
	return roleNone
}

// Package path used to identify our own handle types via reflection.
// Shared by every classifyField helper below so they reference the
// same canonical string.
const viaPkgPath = "github.com/go-via/via"

// Marker interface reflect.Types. Cached once because reflect.TypeOf
// on an interface allocates each call.
var (
	signalMarkerType    = reflect.TypeOf((*signalMarker)(nil)).Elem()
	stateTabMarkerType  = reflect.TypeOf((*stateTabMarker)(nil)).Elem()
	stateSessMarkerType = reflect.TypeOf((*stateSessMarker)(nil)).Elem()
	stateAppMarkerType  = reflect.TypeOf((*stateAppMarker)(nil)).Elem()

	stateAppEventsMarkerType = reflect.TypeOf((*stateAppEventsMarker)(nil)).Elem()
)

// implements reports whether *t (pointer-to-t) implements iface. Used
// to detect handle kinds through their unexported marker methods,
// which promote across embedded specialized wrappers (SignalNum,
// StateTabBool, etc.) for free.
func implements(t, iface reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	if t.PkgPath() != viaPkgPath {
		return false
	}
	return reflect.PointerTo(t).Implements(iface)
}

func isStateSessType(t reflect.Type) bool { return implements(t, stateSessMarkerType) }
func isStateAppType(t reflect.Type) bool  { return implements(t, stateAppMarkerType) }

func isStateAppEventsType(t reflect.Type) bool { return implements(t, stateAppEventsMarkerType) }

// isChildComposition reports whether t is a struct (or pointer-to-struct)
// in a third-party package whose pointer type implements via.Composition.
// Path-tag and Signal/State handles are special-cased earlier and do not
// recurse here. We exclude types whose package matches our own to avoid
// recursing into Signal[T]/StateTab[T]'s internal struct.
func isChildComposition(t reflect.Type) bool {
	tt := t
	if tt.Kind() == reflect.Pointer {
		tt = tt.Elem()
	}
	if tt.Kind() != reflect.Struct {
		return false
	}
	// our own handle types (Signal[T], StateTab[T]) live in the via package;
	// skip them so we don't recurse into private fields.
	if tt.PkgPath() == viaPkgPath {
		return false
	}
	ptr := reflect.PointerTo(tt)
	_, hasView := ptr.MethodByName("View")
	return hasView
}

// isSignalType / isStateType detect handle kinds via unexported marker
// interface methods (isSignal / isStateTab). Specialized wrappers
// (SignalNum[T], StateTabBool, ...) inherit the marker via embedded
// promotion, so the same check works for both generic and specialized
// shapes.
func isSignalType(t reflect.Type) bool { return implements(t, signalMarkerType) }
func isStateType(t reflect.Type) bool  { return implements(t, stateTabMarkerType) }

// qualify joins a dotted path prefix and a name into a wire key. Returns
// the bare name when the prefix is empty so the top-level composition's
// signals stay one level deep.
func qualify(prefix, name string) string {
	if prefix == "" {
		return name
	}
	return prefix + "." + name
}

func parseLocalID(f reflect.StructField) string {
	if tag := f.Tag.Get("via"); tag != "" {
		// Only the first comma-separated segment is the wire key — the
		// rest is options like `init=…`. strings.Cut is one linear scan,
		// no slice allocation.
		if name, _, _ := strings.Cut(tag, ","); name != "" {
			return name
		}
	}
	return lowerFirst(f.Name)
}

func validateViaTagOptions(owner reflect.Type, f reflect.StructField) {
	tag := f.Tag.Get("via")
	if tag == "" {
		return
	}
	first := true
	for part := range strings.SplitSeq(tag, ",") {
		if first {
			first = false
			continue
		}
		if part == "" {
			continue
		}
		if strings.HasPrefix(part, "init=") {
			continue
		}
		panic(fmt.Sprintf(
			"via: %s.%s has unknown via-tag option %q (only `init=…` is recognised)",
			owner.Name(), f.Name, part,
		))
	}
}

func parseInitTag(f reflect.StructField) string {
	tag := f.Tag.Get("via")
	if tag == "" {
		return ""
	}
	// SplitSeq (Go 1.24+) avoids the []string allocation strings.Split
	// would make to hold every comma-separated segment.
	for part := range strings.SplitSeq(tag, ",") {
		if v, ok := strings.CutPrefix(part, "init="); ok {
			return v
		}
	}
	return ""
}
