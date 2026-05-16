package via

import (
	"reflect"
	"sync"
)

// signalKind identifies the field's storage flavor for the descriptor walk.
type signalKind int

const (
	kindSignal signalKind = iota
	kindState
)

type signalSlot struct {
	fieldPath []int // index path from root *C
	kind      signalKind
	wireKey   string
	initRaw   string
}

// scopeBinder is implemented by scope.User[T] / scope.App[T] (pointer
// receiver) so the runtime can write the wire key into the handle's
// unexported storage without going through reflect.FieldByName.
type scopeBinder interface{ BindWireKey(string) }

type scopeSlot struct {
	fieldPath []int  // index path from root *C
	wireKey   string // session/app store key
}

// kindedSlot is the shared shape for path:"…" and query:"…" tagged
// fields. They differ only in source (r.PathValue vs r.URL.Query); the
// slot data itself is identical.
type kindedSlot struct {
	fieldPath []int
	name      string
	kind      reflect.Kind
}

type actionSlot struct {
	name        string
	methodIndex int
	voidReturn  bool // true if the method has signature func(*Ctx) (no error)
}

type cmpDescriptor struct {
	typ          reflect.Type
	route        string
	signalSlots  []signalSlot
	scopeSlots   []scopeSlot
	paramSlots   []kindedSlot
	querySlots   []kindedSlot
	fileSlots    []fileSlot
	actionSlots  []actionSlot
	actionByName map[string]int
	viewIdx      int // method index of View on *C
	initIdx      int // method index of OnInit or -1
	connectIdx   int // method index of OnConnect or -1
	disposeIdx   int // method index of OnDispose or -1

	groupMW []Middleware // middleware from the owning Group, if any
}

var (
	descriptorMu    sync.RWMutex
	descriptorCache = map[reflect.Type]*cmpDescriptor{}
)
