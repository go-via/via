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

// scopeBinder is implemented by StateSess[T] / StateApp[T] (pointer
// receiver) so the runtime can write the wire key into the handle's
// unexported storage without going through reflect.FieldByName.
// The method is unexported so the binding seam is package-private —
// application code can't reach in and desync a handle from its slot.
type scopeBinder interface{ bindWireKey(string) }

// appBinder is implemented by scope handles that need the *App bound at Mount,
// not just the wire key: StateApp (its value cell + changes-feed tailer) and
// StateAppEvents (its per-key projector). The runtime calls bindApp on every
// scope handle that implements it. StateSess does not — it reaches the App
// through the ctx.
type appBinder interface{ bindApp(*App) }

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

	// bind runs validateBindings a single time per composition type (the
	// by-value child clobber is deterministic per type), caching the verdict so
	// the per-render cost amortizes to ~zero. A POINTER so per-mount clones
	// (clone := *desc) share one guard — and so the descriptor stays copyable
	// (a sync.Once value would trip vet copylocks on the clone).
	bind *bindGuard
}

type bindGuard struct {
	once sync.Once
	err  error
}

var (
	descriptorMu    sync.RWMutex
	descriptorCache = map[reflect.Type]*cmpDescriptor{}
)
