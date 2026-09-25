package via

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
// The id hashes the func's Go name plus its receiver's byte offset, not its
// code pointer: the name survives a rebuild, so a deploy does not invalidate
// every action URL an open tab is holding, while the offset keeps two
// instances of one type (struct{ A, B Counter }) from minting one id for A.Inc
// and B.Inc. A receiver not addressable inside the composition (a closure, a
// child behind a pointer or slice field, a value receiver) has no offset and
// falls back to the name alone; two such handlers that hash alike would
// last-wins misroute, so claimAction panics instead.
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
		panic("via: two different actions share the action id " + id + ": " + prev.name +
			" and " + name + " — their receivers are not addressable inside this unit " +
			"(a closure, or a child held through a pointer/slice field), so via cannot tell " +
			"them apart; give each child its own via.Child")
	}
	return id, name, action{name: name, handle: ah, args: prev.args}
}

// actionHandle is a handler's runtime identity within one render: its code
// pointer plus the bound receiver (method value) or funcval. Never hashed into
// the id — both halves move every request — only compared, to turn a would-be
// silent last-wins overwrite into a panic.
type actionHandle struct {
	code uintptr
	self unsafe.Pointer
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

// methodRecv reads the captured receiver. Valid only for a "-fm" func value —
// see funcSelf.
func methodRecv(self unsafe.Pointer) unsafe.Pointer {
	if self == nil {
		return nil
	}
	return *(*unsafe.Pointer)(unsafe.Add(self, unsafe.Sizeof(uintptr(0))))
}

type trampolineCanary struct{}

func (*trampolineCanary) probe() {}

var trampolineVerified sync.Once

// verifyMethodTrampoline panics if the running Go toolchain's "-fm" method-
// value layout no longer matches what actionID assumes. The suffix and the
// receiver read are two independent halves of that assumption — funcName
// checking only the suffix would let a layout change past a string match and
// straight into methodRecv's out-of-bounds pointer read, so both are proved
// here, once per process, before any action id is minted. Every Mount in the
// test suite calls this; a broken layout panics the suite immediately, so no
// dedicated test is needed here.
func verifyMethodTrampoline() {
	trampolineVerified.Do(func() {
		c := &trampolineCanary{}
		fn := c.probe
		name := funcName(reflect.ValueOf(fn).Pointer()).name
		self := funcSelf(fn)
		recv := methodRecv(self)
		if !strings.HasSuffix(name, "-fm") || recv != unsafe.Pointer(c) {
			panic(fmt.Sprintf("via: method-value trampoline canary failed on %s — got name %q, "+
				"receiver %p, want a \"-fm\" suffix and receiver %p. via's action identity depends on "+
				"a method value's runtime layout; a Go toolchain change broke that assumption. File a bug.",
				runtime.Version(), name, recv, c))
		}
	})
}

// actionID content-addresses a handler by its Go name ("main.(*Poll).Vote-fm")
// plus, when the receiver lies inside this unit's composition, that receiver's
// byte offset — the name alone drops the receiver, so two instances of one
// type would collapse onto a single action.
func (c *Ctx) actionID(fn any) (id, name string, ah actionHandle) {
	pc := reflect.ValueOf(fn).Pointer()
	fnName := funcName(pc)
	name, isMethod := fnName.name, fnName.method
	self := funcSelf(fn)
	ah = actionHandle{code: pc, self: self}
	off, scoped := uintptr(0), false
	if isMethod {
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
	return actionIDFor(pc, name, off, scoped), name, ah
}

// funcMeta is the per-code-pointer half of the actionID memo; both halves
// are fixed by the PC.
type funcMeta struct {
	name   string
	method bool
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
	funcNames.Store(pc, info)
	return info
}

type actionIDKey struct {
	pc     uintptr
	off    uintptr
	scoped bool
}

var actionIDs sync.Map // actionIDKey -> string

func actionIDFor(pc uintptr, name string, off uintptr, scoped bool) string {
	k := actionIDKey{pc: pc, off: off, scoped: scoped}
	if v, ok := actionIDs.Load(k); ok {
		return v.(string)
	}
	key := name
	if scoped {
		key = name + "@" + strconv.FormatUint(uint64(off), 10)
	}
	id := hashID(key)
	actionIDs.Store(k, id)
	return id
}

func hashID(key string) string {
	sum := sha256.Sum256([]byte(key))
	return base64.RawURLEncoding.EncodeToString(sum[:])[:8]
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
