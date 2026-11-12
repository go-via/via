// Package actionid is how via names an action and a child on the wire: an
// action id hashes the handler's Go name and its receiver's field path in the
// unit, and a child key is the child's field path from the root. It is split
// out of root so vt can address a handler or a child the same way via does,
// without a second copy of the rules.
package actionid

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"
)

// eface is the runtime layout of an interface value; for a func in an any,
// data is the *funcval.
type eface struct{ typ, data unsafe.Pointer }

// Self returns the func value's *funcval.
//
// A funcval is a code pointer followed by the captured words, and the compiler
// heap-allocates a method-value wrapper even for a zero-sized receiver — so
// the second word is in bounds for a "-fm" func and only for one. A plain func
// or capture-free closure is a one-word static symbol; reading past it would
// be out of bounds, so those are identified by the pointer itself, never
// dereferenced.
func Self(fn any) unsafe.Pointer { return (*eface)(unsafe.Pointer(&fn)).data }

// Recv reads the captured receiver. Valid only for a pointer-receiver
// "-fm" func value — see Self and NoReceiverAddress.
func Recv(self unsafe.Pointer) unsafe.Pointer {
	if self == nil {
		return nil
	}
	return *(*unsafe.Pointer)(unsafe.Add(self, unsafe.Sizeof(uintptr(0))))
}

type trampolineCanary struct{}

func (*trampolineCanary) probe() {}

var trampolineVerified sync.Once

// VerifyTrampoline panics if the running Go toolchain's "-fm" method-
// value layout no longer matches what ID and Targets assume. The name and the
// receiver read are independent halves of that assumption: a layout change
// past a name match reaches Recv's out-of-bounds read, and a naming
// change turns every pointer-receiver handler into a "no receiver address"
// panic, so both are proved once per process before any action id is minted.
// Every Mount in the test suite calls this, so no dedicated test is needed.
func VerifyTrampoline() { trampolineVerified.Do(checkMethodTrampoline) }

func checkMethodTrampoline() {
	c := &trampolineCanary{}
	fn := c.probe
	meta := FuncOf(reflect.ValueOf(fn).Pointer())
	self := Self(fn)
	recv := Recv(self)
	if !meta.Ptr || meta.RecvType != "trampolineCanary" || recv != unsafe.Pointer(c) {
		panic(fmt.Sprintf("via: method-value trampoline canary failed on %s — got name %q, "+
			"receiver %p, want a \"pkg.(*trampolineCanary).probe-fm\" name and receiver %p. via's "+
			"action identity depends on a method value's name and runtime layout; a Go toolchain "+
			"change broke that assumption. File a bug.",
			runtime.Version(), meta.Name, recv, c))
	}
}

// NoReceiverAddress says why a method value named "pkg.T.M-fm" has no id. That
// name is either a value receiver, whose funcval captures a copy of T, or an
// interface T, whose funcval captures the itab and data words; neither holds a
// receiver address to key the action by, and a value receiver's changes would
// land on the copy.
func NoReceiverAddress(meta Func) string {
	trimmed := strings.TrimSuffix(meta.Name, "-fm")
	method := trimmed[strings.LastIndexByte(trimmed, '.')+1:]
	recv := meta.RecvType
	if strings.Contains(meta.Name, "[...]") {
		recv += "[T]"
	}
	return "via: action handler " + meta.Name + " has a value receiver or is taken through an interface: " +
		"it carries no receiver address, so via cannot tell which field a click meant, and a value " +
		"receiver's changes land on a copy. Give it a pointer receiver (func (*" + recv + ") " + method +
		"), and hold an interface field by its concrete type."
}

// splitMethodName splits "pkg/path.T.M-fm" or "pkg/path.(*T[...]).M-fm" into
// the package path and the receiver's base type name.
func splitMethodName(name string) (pkg, typ string) {
	name = strings.TrimSuffix(name, "-fm")
	i := strings.LastIndexByte(name, '.')
	if i < 0 {
		return "", ""
	}
	recv := name[:i]
	if j := strings.Index(recv, ".(*"); j >= 0 && strings.HasSuffix(recv, ")") {
		return recv[:j], baseTypeName(recv[j+3 : len(recv)-1])
	}
	recv = baseTypeName(recv) // "pkg.G[...]": the type arguments hold dots of their own
	if j := strings.LastIndexByte(recv, '.'); j >= 0 {
		return recv[:j], recv[j+1:]
	}
	return "", recv
}

// baseTypeName drops type arguments, which runtime names spell "[...]" and
// reflect spells out ("G[int]").
func baseTypeName(n string) string {
	if i := strings.IndexByte(n, '['); i >= 0 {
		return n[:i]
	}
	return n
}

func isType(t reflect.Type, pkg, typ string) bool {
	return t.PkgPath() == pkg && baseTypeName(t.Name()) == typ
}

// Func is the per-code-pointer half of the ID memo; all of it is
// fixed by the PC.
type Func struct {
	Name   string
	Method bool // a method value ("-fm")
	// Ptr marks a pointer receiver, "pkg.(*T).M-fm": the only method value
	// whose first captured word is the receiver's address.
	Ptr               bool
	RecvPkg, RecvType string
}

var funcNames sync.Map // uintptr (code pointer) -> Func

// FuncOf resolves a code pointer's Go name once per process:
// runtime.FuncForPC walks the module's pclntab, which at a thousand bindings
// is a measurable slice of every render.
func FuncOf(pc uintptr) Func {
	if v, ok := funcNames.Load(pc); ok {
		return v.(Func)
	}
	info := Func{Name: "unknown"}
	if f := runtime.FuncForPC(pc); f != nil {
		info.Name = f.Name()
	}
	info.Method = strings.HasSuffix(info.Name, "-fm")
	if info.Method {
		info.Ptr = strings.Contains(info.Name, ".(*")
		info.RecvPkg, info.RecvType = splitMethodName(info.Name)
	}
	funcNames.Store(pc, info)
	return info
}

// idKey carries the unit type because an offset means a field path only
// within it.
type idKey struct {
	unit   reflect.Type
	pc     uintptr
	off    uintptr
	scoped bool
}

var ids sync.Map // idKey -> string

// ID is the wire id of the handler at pc: its Go name, plus, when scoped, the
// field path in unit of the receiver at byte offset off. Memoized per process.
func ID(unit reflect.Type, pc uintptr, meta Func, off uintptr, scoped bool) string {
	k := idKey{unit: unit, pc: pc, off: off, scoped: scoped}
	if v, ok := ids.Load(k); ok {
		return v.(string)
	}
	key := meta.Name
	if scoped {
		path := fieldPath(unit, off, meta.RecvPkg, meta.RecvType)
		if path == "" {
			// The key the unit's own methods had before field paths: changing
			// it would 410 every button on the tabs open across a deploy.
			path = "0"
		}
		key += "@" + path
	}
	id := hash(key)
	ids.Store(k, id)
	return id
}

// fieldPath names the field of t at byte offset off that holds a receiver of
// type pkg.typ: "" for t itself, "A", "Stats.A", "Rows[1]". Descent follows the
// offset, so receivers at different offsets always get different paths; the
// type check only decides where along a run of fields sharing an offset (a
// struct and its first field) the receiver sits.
func fieldPath(t reflect.Type, off uintptr, pkg, typ string) string {
	var path strings.Builder
	for t != nil && (off != 0 || !isType(t, pkg, typ)) {
		switch t.Kind() {
		case reflect.Struct:
			f, ok := fieldAt(t, off, pkg, typ)
			if !ok {
				t = nil
				continue
			}
			if path.Len() > 0 {
				path.WriteByte('.')
			}
			path.WriteString(f.Name)
			off -= f.Offset
			t = f.Type
		case reflect.Array:
			size := t.Elem().Size()
			if size == 0 {
				t = nil
				continue
			}
			i := off / size
			path.WriteString("[" + strconv.FormatUint(uint64(i), 10) + "]")
			off -= i * size
			t = t.Elem()
		default:
			t = nil
		}
	}
	if off != 0 {
		path.WriteString("+" + strconv.FormatUint(uint64(off), 10))
	}
	return path.String()
}

// fieldAt is the field of struct t that contains byte offset off. A zero-sized
// field contains no byte, so one at off is chosen only when it is of the
// receiver's own type.
func fieldAt(t reflect.Type, off uintptr, pkg, typ string) (reflect.StructField, bool) {
	var hit reflect.StructField
	found := false
	for i := range t.NumField() {
		f := t.Field(i)
		size := f.Type.Size()
		switch {
		case size == 0 && f.Offset == off && isType(f.Type, pkg, typ):
			return f, true
		case !found && f.Offset <= off && off < f.Offset+size:
			hit, found = f, true
		}
	}
	return hit, found
}

const idLen = 8

func hash(key string) string {
	sum := sha256.Sum256([]byte(key))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:idLen]
}

// Valid reports whether act has the shape hash mints.
func Valid(act string) bool {
	if len(act) != idLen {
		return false
	}
	for i := range len(act) {
		switch c := act[i]; {
		case 'a' <= c && c <= 'z', 'A' <= c && c <= 'Z', '0' <= c && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

// ChildPath names the field of t at byte offset off that holds a target, with
// '-' between struct fields and array indexes. It walks struct fields and array
// elements only: memory behind a pointer, slice or map is not t's. An embedded
// struct keeps its name ("page-Search" for a promoted p.Search): an outer field
// may shadow the promoted one, and the two must not share a key.
func ChildPath(t reflect.Type, off uintptr, target reflect.Type) (string, bool) {
	var segs []string
	for off != 0 || t != target {
		switch t.Kind() {
		case reflect.Struct:
			f, ok := fieldHolding(t, off, target)
			if !ok {
				return "", false
			}
			segs = append(segs, f.Name)
			off -= f.Offset
			t = f.Type
		case reflect.Array:
			size := t.Elem().Size()
			if size == 0 {
				return "", false
			}
			i := off / size
			if i >= uintptr(t.Len()) {
				return "", false
			}
			segs = append(segs, strconv.FormatUint(uint64(i), 10))
			off -= i * size
			t = t.Elem()
		default:
			return "", false
		}
	}
	if len(segs) == 0 {
		return "", false // off names t itself, not a field of it
	}
	return strings.Join(segs, "-"), true
}

// fieldHolding is the field of struct t whose bytes contain off. A zero-sized
// field contains no byte, so one at off is chosen only when it is a target.
func fieldHolding(t reflect.Type, off uintptr, target reflect.Type) (reflect.StructField, bool) {
	for i := range t.NumField() {
		f := t.Field(i)
		size := f.Type.Size()
		if size == 0 && f.Offset == off && f.Type == target {
			return f, true
		}
		if f.Offset <= off && off < f.Offset+size {
			return f, true
		}
	}
	return reflect.StructField{}, false
}

// RootUnit is the root's address in an action URL. No child key can be "_": a
// blank field cannot have its address taken.
const RootUnit = "_"

// Target is one place an action may sit on the wire: the unit address in its
// URL and its id there.
type Target struct{ Unit, ID string }

// Targets lists where fn, a handler taken off root (a pointer to the mounted
// composition), may sit on the wire, deepest unit first: the root, and each
// field on the receiver's path whose pointer has a View, since via.Child may
// render it as its own unit. Unit is "" when the receiver lies outside root,
// whose id carries no unit. It panics on a method value with no receiver
// address, as via does when it binds one.
func Targets(root, fn any) []Target {
	pc := reflect.ValueOf(fn).Pointer()
	meta := FuncOf(pc)
	if meta.Method && !meta.Ptr {
		panic(NoReceiverAddress(meta))
	}
	rv := reflect.ValueOf(root)
	t, base := rv.Type().Elem(), rv.Pointer()
	off := t.Size() // a plain func or closure has no receiver in root
	if meta.Method {
		off = uintptr(Recv(Self(fn))) - base
	}
	if off >= t.Size() {
		return []Target{{ID: ID(t, pc, meta, 0, false)}}
	}
	out := []Target{{Unit: RootUnit, ID: ID(t, pc, meta, off, true)}}
	cur, at := t, uintptr(0)
	for {
		next, start, ok := holder(cur, off-at)
		if !ok {
			return out
		}
		cur, at = next, at+start
		if _, ok := reflect.PointerTo(cur).MethodByName("View"); ok {
			if key, ok := ChildPath(t, at, cur); ok {
				out = append([]Target{{Unit: key, ID: ID(cur, pc, meta, off-at, true)}}, out...)
			}
		}
	}
}

// holder is the field or array element of t that holds byte off, and where it
// starts within t.
func holder(t reflect.Type, off uintptr) (reflect.Type, uintptr, bool) {
	switch t.Kind() {
	case reflect.Struct:
		f, ok := fieldHolding(t, off, nil)
		return f.Type, f.Offset, ok
	case reflect.Array:
		size := t.Elem().Size()
		if size == 0 {
			return nil, 0, false
		}
		return t.Elem(), off / size * size, true
	}
	return nil, 0, false
}

// Key is the child key via gives child, a pointer into root (a pointer to the
// mounted composition): its field path from the root.
func Key(root, child any) (string, bool) {
	rv, cv := reflect.ValueOf(root), reflect.ValueOf(child)
	return ChildPath(rv.Type().Elem(), cv.Pointer()-rv.Pointer(), cv.Type().Elem())
}
