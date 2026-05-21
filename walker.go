package via

import (
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
		switch role := classifyField(f); role {
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
		case roleStateSess, roleStateApp:
			d.scopeSlots = append(d.scopeSlots, scopeSlot{
				fieldPath: fieldPath,
				wireKey:   qualify(pathPrefix, parseLocalID(f)),
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

func isStateSessType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return strings.HasPrefix(t.Name(), "StateSess[") && t.PkgPath() == viaPkgPath
}

func isStateAppType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return strings.HasPrefix(t.Name(), "StateApp[") && t.PkgPath() == viaPkgPath
}

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

// isSignalType returns true if t is a Signal[T] for some T. We detect by
// the type name prefix to avoid relying on a sentinel field.
func isSignalType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return strings.HasPrefix(t.Name(), "Signal[") && t.PkgPath() == viaPkgPath
}

func isStateType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return strings.HasPrefix(t.Name(), "StateTab[") && t.PkgPath() == viaPkgPath
}

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
