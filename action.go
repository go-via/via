package via

import (
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"hash/fnv"
	"reflect"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"unsafe"

	"github.com/go-via/via/internal/hcore"
)

type action struct {
	fn     func(*Ctx)
	name   string       // the Go name, so a 410 on a vanished handler reads as a name, not a bare id
	handle actionHandle // runtime identity, compared to catch an id collision
	// args is the set of ?a= payloads this render bound for the handler, in the
	// exact JSON the binding shipped. nil marks an argless action (On/PostForm).
	args map[string]struct{}
}

// actionSlot registers run under the content-addressed id of ident — for
// OnArg that is the user's fn, not the decoding wrapper, which would name the
// same via-internal closure for every row.
//
// The id hashes the func's Go name plus the field path of its receiver inside
// the unit ("A", "Stats.A", "Rows[1]"), not its code pointer or byte offset:
// the name survives a rebuild, so a deploy does not invalidate the action URLs
// an open tab holds, and the path keeps struct{ A, B Counter }'s A.Inc and
// B.Inc apart and survives a field reorder. A receiver outside the unit's
// struct (a closure, a child behind a pointer or slice field) has no path and
// falls back to the name alone; two such handlers that hash alike would
// last-wins misroute, so claimSlot panics instead. A value-receiver or
// interface method value has no receiver address: valueMethodID keys it by its
// type's one path in the unit, or refuses it.
func (c *Ctx) actionSlot(run func(*Ctx)) string {
	id, _, a := c.claimSlot(run)
	a.fn = run
	c.actions[id] = a
	return id
}

// actionSlotArg is actionSlot for a value-carrying action: it records arg in
// the slot's allowed set, so dispatch can reject a (handler, arg) pair this
// render never produced. The set is created once per slot and mutated in place
// as later rows claim it, so the closure stored here (the last row's) closes
// over the finished set no matter which row wrote it.
func (c *Ctx) actionSlotArg(ident any, run func(rc *Ctx, name string, bound map[string]struct{}), arg string) string {
	id, name, a := c.claimSlot(ident)
	if a.args == nil {
		a.args = map[string]struct{}{}
	}
	a.args[arg] = struct{}{}
	args := a.args
	a.fn = func(rc *Ctx) { run(rc, name, args) }
	c.actions[id] = a
	return id
}

// claimSlot resolves ident's action id and rejects a collision between two
// handlers via cannot tell apart, returning the id, the handler name, and the
// row to fill in (carrying any args a previous claim on this id recorded).
func (c *Ctx) claimSlot(ident any) (string, string, action) {
	id, name, ah := c.actionID(ident)
	prev, dup := c.actions[id]
	if dup && prev.handle != ah {
		panic(hcore.Miswired(sharedID(id, prev.name, name)))
	}
	return id, name, action{name: name, handle: ah, args: prev.args}
}

// sharedID leads with the likelier cause, read off the name: a func literal is
// identified by its code alone, so one bound per row collides with itself; a
// method collides when its receivers sit outside the unit's struct.
func sharedID(id, prev, name string) string {
	msg := "via: two different actions share the action id " + id + ": " + prev + " and " + name
	withArg := "bind a method and carry the row with on.WithArg(p.Method, row.ID)"
	child := "hold each receiver as a direct struct field (not behind a pointer, slice or map), " +
		"rendering a child through its own via.Child"
	if !strings.HasSuffix(name, "-fm") {
		return msg + " — likely a func literal bound once per row (in Each or a loop). via identifies " +
			"a func literal by its code, so the copies look alike. Instead, " + withArg +
			". If each copy belongs to a separate receiver, " + child + "."
	}
	return msg + " — their receivers are outside this unit's struct (reached through a pointer, " +
		"slice or map field), so via has no field to tell them apart by. Instead, " + child +
		". For one handler per row, " + withArg + "."
}

// actionHandle is a handler's runtime identity within one render: its code
// pointer plus the bound receiver (method value) or funcval. Never hashed into
// the id — both halves move every request — only compared, to turn a would-be
// silent last-wins overwrite into a panic.
type actionHandle struct {
	code uintptr
	self unsafe.Pointer
	copy uint64 // a value receiver's copy, which has no address to compare (see valueMethodID)
}

// eface is the runtime layout of an interface value; for a func in an any,
// data is the *funcval.
type eface struct{ typ, data unsafe.Pointer }

// funcSelf returns the func value's *funcval.
//
// A funcval is a code pointer followed by the captured words, and the compiler
// heap-allocates a method-value wrapper even for a zero-sized receiver — so
// the second word is in bounds for a "-fm" func and only for one. A plain func
// or capture-free closure is a one-word static symbol; reading past it would
// be out of bounds, so those are identified by the pointer itself, never
// dereferenced.
func funcSelf(fn any) unsafe.Pointer { return (*eface)(unsafe.Pointer(&fn)).data }

// methodRecv reads the captured receiver. Valid only for a pointer-receiver
// "-fm" func value — see funcSelf and valueMethodID.
func methodRecv(self unsafe.Pointer) unsafe.Pointer {
	if self == nil {
		return nil
	}
	return *(*unsafe.Pointer)(unsafe.Add(self, unsafe.Sizeof(uintptr(0))))
}

type trampolineCanary struct{}

func (*trampolineCanary) probe() {}

type valueCanary struct{ a, b uintptr }

func (valueCanary) probe() {}

var trampolineVerified sync.Once

// verifyMethodTrampoline panics if the running Go toolchain's "-fm" method-
// value layout no longer matches what actionID assumes. The name and the
// receiver read are independent halves of that assumption: a layout change
// past a name match reaches methodRecv's out-of-bounds read, and a naming
// change turns every pointer-receiver handler into a "no receiver address"
// panic, so both are proved once per process before any action id is minted.
// Every Mount in the test suite calls this, so no dedicated test is needed.
func verifyMethodTrampoline() {
	trampolineVerified.Do(func() {
		c := &trampolineCanary{}
		fn := c.probe
		meta := funcName(reflect.ValueOf(fn).Pointer())
		self := funcSelf(fn)
		recv := methodRecv(self)
		if !meta.ptr || meta.recvType != "trampolineCanary" || recv != unsafe.Pointer(c) {
			panic(fmt.Sprintf("via: method-value trampoline canary failed on %s — got name %q, "+
				"receiver %p, want a \"pkg.(*trampolineCanary).probe-fm\" name and receiver %p. via's "+
				"action identity depends on a method value's name and runtime layout; a Go toolchain "+
				"change broke that assumption. File a bug.",
				runtime.Version(), meta.name, recv, c))
		}
		v := valueCanary{a: 0x5a5a, b: 0xa5a5}
		vfn := v.probe
		vmeta := funcName(reflect.ValueOf(vfn).Pointer())
		held := (*valueCanary)(unsafe.Add(funcSelf(vfn), unsafe.Sizeof(uintptr(0))))
		if !vmeta.method || vmeta.ptr || vmeta.recvType != "valueCanary" || *held != v {
			panic(fmt.Sprintf("via: value-receiver method-value canary failed on %s — got name %q; "+
				"via's action identity depends on a value receiver's copy sitting right after the "+
				"code pointer. A Go toolchain change broke that assumption. File a bug.",
				runtime.Version(), vmeta.name))
		}
	})
}

// actionID content-addresses a handler by its Go name ("main.(*Poll).Vote-fm")
// plus, when the receiver lies inside this unit's composition, that receiver's
// field path — the name alone drops the receiver, so two instances of one type
// would collapse onto a single action. A method value with no receiver address
// is keyed by valueMethodID instead.
func (c *Ctx) actionID(fn any) (id, name string, ah actionHandle) {
	pc := reflect.ValueOf(fn).Pointer()
	meta := funcName(pc)
	self := funcSelf(fn)
	ah = actionHandle{code: pc, self: self}
	off, scoped := uintptr(0), false
	if meta.method && !meta.ptr {
		v := valueMethodID(c.unitV.typ, pc, meta)
		if v.refusal != "" {
			panic(hcore.Miswired(v.refusal))
		}
		return v.id, meta.name, actionHandle{code: pc, copy: v.copyDigest(self)}
	}
	if meta.method {
		recv := methodRecv(self)
		ah.self = recv // two takes of the same method value are two funcvals; the receiver is the identity
		if base := c.unitV.base; base != nil && recv != nil {
			// Unsigned, so a receiver below the base wraps past size and fails
			// the bound check along with one above it.
			if o := uintptr(recv) - uintptr(base); o < c.unitV.size {
				off, scoped = o, true
			}
		}
	}
	return actionIDFor(c.unitV.typ, pc, meta, off, scoped), meta.name, ah
}

// valueID is the memoized verdict on a value-receiver or interface method
// value within one unit type: its id, or why it has none.
type valueID struct {
	id      string
	refusal string
	off     uintptr // where the funcval holds the receiver copy
	size    uintptr
}

type valueIDKey struct {
	unit reflect.Type
	pc   uintptr
}

var valueIDs sync.Map // valueIDKey -> valueID

// valueMethodID keys a method value named "pkg.T.M-fm". That name is either a
// value receiver, whose funcval captures a copy of T with no address, or an
// interface T, whose funcval captures the itab and data words; neither's first
// word is a receiver address.
//
// With no address, the type is all there is to go on. An interface is refused:
// the value behind it is not a field of the unit, so there is no path to key
// by. A value receiver whose type the unit holds at exactly one path (the unit
// itself, or one field) is keyed by that path. Held at two paths, or at none
// (a slice element, a local), via cannot say which copy a click meant, so it
// is refused too.
func valueMethodID(unit reflect.Type, pc uintptr, meta funcMeta) valueID {
	k := valueIDKey{unit: unit, pc: pc}
	if v, ok := valueIDs.Load(k); ok {
		return v.(valueID)
	}
	paths, t, mixed := typePaths(unit, meta.recvPkg, meta.recvType)
	var v valueID
	switch {
	case t != nil && t.Kind() == reflect.Interface, len(paths) != 1:
		v.refusal = noReceiverAddress(meta, paths, t, mixed)
	default:
		path := paths[0]
		if path == "" {
			path = "0" // the same key a pointer receiver on the unit gets
		}
		v.id = hashID(meta.name + "@" + path)
		ptr := unsafe.Sizeof(uintptr(0))
		align := uintptr(t.Align())
		v.off, v.size = (ptr+align-1)/align*align, t.Size()
	}
	valueIDs.Store(k, v)
	return v
}

// copyDigest fingerprints the receiver copy a value-receiver funcval holds.
// The path proves which field the type sits at, not that this copy came from
// it: a copy taken off a slice row keys the same. Two copies that differ are
// two handlers via cannot tell apart, and claimSlot sees different handles.
func (v valueID) copyDigest(self unsafe.Pointer) uint64 {
	if self == nil || v.size == 0 {
		return 0
	}
	h := fnv.New64a()
	h.Write(unsafe.Slice((*byte)(unsafe.Add(self, v.off)), v.size))
	return h.Sum64()
}

// noReceiverAddress says why a method value has no id. mixed means the paths
// hold different instantiations of one generic type: the runtime names every
// instantiation's method "T[...].M-fm", and instantiations that share a GC
// shape share one compiled body, so neither the name nor the code tells which
// one a method value came from.
func noReceiverAddress(meta funcMeta, paths []string, t reflect.Type, mixed bool) string {
	name, typ := meta.name, meta.recvType
	trimmed := strings.TrimSuffix(name, "-fm")
	method := trimmed[strings.LastIndexByte(trimmed, '.')+1:]
	where := ""
	switch len(paths) {
	case 0:
	case 1:
		where = " (field " + paths[0] + ")"
	default:
		where = " (fields " + strings.Join(paths, ", ") + ")"
	}
	recv := typ
	if strings.Contains(name, "[...]") {
		recv += "[T]"
	}
	fix := "Give it a pointer receiver: func (*" + recv + ") " + method + "."
	switch {
	case t != nil && t.Kind() == reflect.Interface:
		return "via: action handler " + name + " is a method taken through an interface" + where +
			": it carries no receiver address, and the value behind the interface is not a field of " +
			"this unit, so via has nothing to key it by and a click could run another field's handler. " +
			"Declare the field with its concrete type and bind a method with a pointer receiver on it."
	case len(paths) > 1 && mixed:
		return "via: action handler " + name + " has a value receiver on generic type " + typ +
			", and this unit holds instantiations of it" + where + ": they cannot be told apart by name, " +
			"so via cannot tell which field a click meant and could run the other one. " + fix
	case len(paths) > 1:
		return "via: action handler " + name + " has a value receiver and this unit holds " + typ + where +
			": the method runs on a copy with no address, so via cannot tell which field a click " +
			"meant and could run the other one. " + fix
	}
	return "via: action handler " + name + " has a value receiver or is taken through an interface, " +
		"and its receiver is not a field of this unit (a pointer, slice or map element, or a local): " +
		"it carries no receiver address, so via cannot tell such copies apart. " + fix +
		" Hold the receiver as a struct field."
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

// typePaths lists where unit's own memory holds a pkg.typ, walking struct
// fields and array elements as fieldPath does, never through a pointer: ""
// for the unit itself, "A", "Stats.A", "Rows[0]". t is the matched type, and
// mixed reports that the matches are different instantiations of a generic
// typ. An array is walked for at most two elements, which is enough to tell
// one holder from several.
func typePaths(unit reflect.Type, pkg, typ string) (paths []string, t reflect.Type, mixed bool) {
	var walk func(cur reflect.Type, path string)
	walk = func(cur reflect.Type, path string) {
		if isType(cur, pkg, typ) {
			mixed = mixed || t != nil && t != cur
			paths, t = append(paths, path), cur
			return
		}
		switch cur.Kind() {
		case reflect.Struct:
			for i := range cur.NumField() {
				f := cur.Field(i)
				p := f.Name
				if path != "" {
					p = path + "." + f.Name
				}
				walk(f.Type, p)
			}
		case reflect.Array:
			for i := range min(cur.Len(), 2) {
				walk(cur.Elem(), path+"["+strconv.Itoa(i)+"]")
			}
		}
	}
	if unit != nil {
		walk(unit, "")
	}
	return paths, t, mixed
}

// funcMeta is the per-code-pointer half of the actionID memo; all of it is
// fixed by the PC.
type funcMeta struct {
	name   string
	method bool // a method value ("-fm")
	// ptr marks a pointer receiver, "pkg.(*T).M-fm": the only method value
	// whose first captured word is the receiver's address.
	ptr               bool
	recvPkg, recvType string
}

var funcNames sync.Map // uintptr (code pointer) -> funcMeta

// funcName resolves a code pointer's Go name once per process:
// runtime.FuncForPC walks the module's pclntab (~190ns with the sha256 below),
// which at a thousand bindings is a measurable slice of every render.
func funcName(pc uintptr) funcMeta {
	if v, ok := funcNames.Load(pc); ok {
		return v.(funcMeta)
	}
	info := funcMeta{name: "unknown"}
	if f := runtime.FuncForPC(pc); f != nil {
		info.name = f.Name()
	}
	info.method = strings.HasSuffix(info.name, "-fm")
	if info.method {
		info.ptr = strings.Contains(info.name, ".(*")
		info.recvPkg, info.recvType = splitMethodName(info.name)
	}
	funcNames.Store(pc, info)
	return info
}

// actionIDKey carries the unit type because an offset means a field path only
// within it.
type actionIDKey struct {
	unit   reflect.Type
	pc     uintptr
	off    uintptr
	scoped bool
}

var actionIDs sync.Map // actionIDKey -> string

func actionIDFor(unit reflect.Type, pc uintptr, meta funcMeta, off uintptr, scoped bool) string {
	k := actionIDKey{unit: unit, pc: pc, off: off, scoped: scoped}
	if v, ok := actionIDs.Load(k); ok {
		return v.(string)
	}
	key := meta.name
	if scoped {
		path := fieldPath(unit, off, meta.recvPkg, meta.recvType)
		if path == "" {
			// The key the unit's own methods had before field paths: changing
			// it would 410 every button on the tabs open across a deploy.
			path = "0"
		}
		key += "@" + path
	}
	id := hashID(key)
	actionIDs.Store(k, id)
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

const actionIDLen = 8

func hashID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:actionIDLen]
}

// isActionID reports whether act has the shape hashID mints.
func isActionID(act string) bool {
	if len(act) != actionIDLen {
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

// unitIdentParam carries a child's call-site identity (instance.ident) on its
// action URLs, so dispatch can refuse a stale URL whose key now holds another
// child of the same type.
const unitIdentParam = "u"

// actionPath is the POST path of action id on c's unit, unescaped.
func actionPath(c *Ctx, id string) string {
	path := c.base + "/_via/a/" + unitAddr(c) + "/" + id
	if c.unitV.ident != "" {
		path += "?" + unitIdentParam + "=" + c.unitV.ident
	}
	return path
}
