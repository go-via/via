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
	roleScopeUser
	roleScopeApp
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
		case roleScopeUser, roleScopeApp:
			handleType := f.Type
			if handleType.Kind() == reflect.Ptr {
				handleType = handleType.Elem()
			}
			if !reflect.PointerTo(handleType).Implements(reflect.TypeOf((*scopeBinder)(nil)).Elem()) {
				panic("via.Mount: scope handle " + handleType.String() +
					" must implement BindWireKey(string)")
			}
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
			if child.Kind() == reflect.Ptr {
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
	if isScopeUserType(f.Type) {
		return roleScopeUser
	}
	if isScopeAppType(f.Type) {
		return roleScopeApp
	}
	if isFileType(f.Type) {
		return roleFile
	}
	if isChildComposition(f.Type) {
		return roleChild
	}
	return roleNone
}

// Package paths used to identify our own handle types via reflection.
// Stored as constants so the four classifyField helpers below all
// reference the same canonical strings.
const (
	viaPkgPath   = "github.com/go-via/via"
	scopePkgPath = "github.com/go-via/via/scope"
)

func isScopeUserType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return strings.HasPrefix(t.Name(), "User[") && t.PkgPath() == scopePkgPath
}

func isScopeAppType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return strings.HasPrefix(t.Name(), "App[") && t.PkgPath() == scopePkgPath
}

// isChildComposition reports whether t is a struct (or pointer-to-struct)
// in a third-party package whose pointer type implements via.Composition.
// Path-tag and Signal/State handles are special-cased earlier and do not
// recurse here. We exclude types whose package matches our own to avoid
// recursing into Signal[T]/State[T]'s internal struct.
func isChildComposition(t reflect.Type) bool {
	tt := t
	if tt.Kind() == reflect.Ptr {
		tt = tt.Elem()
	}
	if tt.Kind() != reflect.Struct {
		return false
	}
	// our own handle types (Signal[T], State[T]) live in the via package;
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
	return strings.HasPrefix(t.Name(), "State[") && t.PkgPath() == viaPkgPath
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
