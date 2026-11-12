package via

import (
	"errors"
	"reflect"
	"strconv"
	"strings"
	"unsafe"

	"github.com/go-via/via/internal/actionid"
	"github.com/go-via/via/internal/render"
)

type action struct {
	fn     func(*Ctx)     // argless only
	call   render.ArgCall // arg actions (on.Bind) only
	name   string         // the Go name, so a 410 on a vanished handler reads as a name, not a bare id
	handle actionHandle   // runtime identity, compared to catch an id collision
	// args is the set of ?a= payloads this render bound for the handler, in the
	// exact JSON the binding shipped. nil marks an argless action (on.Event,
	// PostForm).
	args map[string]struct{}
}

// actionSlot registers run under the content-addressed id of run itself;
// actionSlotArg does the same for on.Bind's fn, never its ArgCall, which is
// the same via-internal type for every row.
//
// The id hashes the func's Go name plus the field path of its receiver inside
// the unit ("A", "Stats.A", "Rows[1]"), not its code pointer or byte offset:
// the name survives a rebuild, so a deploy does not invalidate the action URLs
// an open tab holds, and the path keeps struct{ A, B Counter }'s A.Inc and
// B.Inc apart and survives a field reorder. A receiver outside the unit's
// struct (a closure, a child behind a pointer or slice field) has no path and
// falls back to the name alone; two such handlers that hash alike would
// last-wins misroute, so claimSlot panics instead. A value-receiver or
// interface method value has no receiver address and is refused.
func (c *Ctx) actionSlot(run func(*Ctx)) string {
	id, a := c.claimSlot(run)
	a.fn = run
	c.actions[id] = a
	return id
}

// actionSlotArg is actionSlot for a value-carrying action: it records arg in
// the slot's allowed set, so dispatch can reject a (handler, arg) pair this
// render never produced. The set is created once per slot and mutated in place
// as later rows claim it, so every row's copy of the action shares it.
func (c *Ctx) actionSlotArg(ident any, call render.ArgCall, arg string) string {
	id, a := c.claimSlot(ident)
	if a.args == nil {
		a.args = map[string]struct{}{}
	}
	a.args[arg] = struct{}{}
	a.call = call
	c.actions[id] = a
	return id
}

// run calls the handler on rc: directly when argless, else with the arg the
// request carries, which must be one this render bound.
func (a action) run(rc *Ctx) {
	if a.call == nil {
		a.fn(rc)
		return
	}
	if rc.req == nil {
		return
	}
	raw := rc.req.URL.Query().Get("a")
	if raw == "" || raw == "null" {
		panic(badActionArg{err: errors.New("missing action arg")})
	}
	v, err := a.call.Decode([]byte(raw))
	if err != nil {
		panic(badActionArg{err: err})
	}
	// The dispatchable-iff-rendered check (see on.Bind). Matching the exact
	// bytes the binding shipped means a client echoing the URL it was served
	// passes, while any arg it invents — even a re-spelling of a legitimate
	// one — fails closed.
	if _, ok := a.args[raw]; !ok {
		panic(unrenderedArg{name: a.name, raw: strconv.Quote(raw), have: len(a.args)})
	}
	a.call.Call(rc, v)
}

// claimSlot resolves ident's action id and rejects a collision between two
// handlers via cannot tell apart, returning the id and the row to fill in
// (carrying any args a previous claim on this id recorded).
func (c *Ctx) claimSlot(ident any) (string, action) {
	id, name, ah := c.actionID(ident)
	prev, dup := c.actions[id]
	if dup && prev.handle != ah {
		panic(render.Miswired(sharedID(id, prev.name, name)))
	}
	return id, action{name: name, handle: ah, args: prev.args}
}

// sharedID leads with the likelier cause, read off the name: a func literal is
// identified by its code alone, so one bound per row collides with itself; a
// method collides when its receivers sit outside the unit's struct.
func sharedID(id, prev, name string) string {
	msg := "via: two different actions share the action id " + id + ": " + prev + " and " + name
	useBind := "bind a method and carry the row with on.Bind(p.Method, row.ID)"
	child := "hold each receiver as a direct struct field (not behind a pointer, slice or map), " +
		"rendering a child through its own via.Child"
	if !strings.HasSuffix(name, "-fm") {
		return msg + " — likely a func literal bound once per row (in Each or a loop). via identifies " +
			"a func literal by its code, so the copies look alike. Instead, " + useBind +
			". If each copy belongs to a separate receiver, " + child + "."
	}
	return msg + " — their receivers are outside this unit's struct (reached through a pointer, " +
		"slice or map field), so via has no field to tell them apart by. Instead, " + child +
		". For one handler per row, " + useBind + "."
}

// actionHandle is a handler's runtime identity within one render: its code
// pointer plus the bound receiver (method value) or funcval. Never hashed into
// the id — both halves move every request — only compared, to turn a would-be
// silent last-wins overwrite into a panic.
type actionHandle struct {
	code uintptr
	self unsafe.Pointer
}

// actionID content-addresses a handler by its Go name ("main.(*Poll).Vote-fm")
// plus, when the receiver lies inside this unit's composition, that receiver's
// field path — the name alone drops the receiver, so two instances of one type
// would collapse onto a single action. A method value with no receiver address
// panics (see actionid.NoReceiverAddress).
func (c *Ctx) actionID(fn any) (id, name string, ah actionHandle) {
	pc := reflect.ValueOf(fn).Pointer()
	meta := actionid.FuncOf(pc)
	self := actionid.Self(fn)
	ah = actionHandle{code: pc, self: self}
	off, scoped := uintptr(0), false
	if meta.Method && !meta.Ptr {
		panic(render.Miswired(actionid.NoReceiverAddress(meta)))
	}
	if meta.Method {
		recv := actionid.Recv(self)
		ah.self = recv // two takes of the same method value are two funcvals; the receiver is the identity
		if base := c.unitV.base; base != nil && recv != nil {
			// Unsigned, so a receiver below the base wraps past size and fails
			// the bound check along with one above it.
			if o := uintptr(recv) - uintptr(base); o < c.unitV.size {
				off, scoped = o, true
			}
		}
	}
	return actionid.ID(c.unitV.typ, pc, meta, off, scoped), meta.Name, ah
}

// actionPath is the POST path of action id on c's unit, unescaped.
func actionPath(c *Ctx, id string) string {
	return c.base + actionRoute + unitAddr(c) + "/" + id
}
