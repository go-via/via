package via

import (
	"fmt"
	"log/slog"
	"reflect"
	"strings"
	"sync"

	"github.com/go-via/via/internal/render"
)

// hookSpecs are via's optional, duck-typed hooks. Opting in is having the
// method; the cost of that is that a typo or a signature drift opts you
// silently out — the composition still compiles and the hook never runs.
// rootOnly marks the ones only a mounted page's own methods are read from.
type hookSpec struct {
	name       string
	want       string
	shaped     func(reflect.Type) bool
	implements func(reflect.Type) bool
	rootOnly   bool
}

var hookSpecs = []hookSpec{
	{name: "OnInit", want: "func(*via.Ctx) error", shaped: ctxErrShaped, implements: implementsAs[initer]},
	{name: "OnReload", want: "func(*via.Ctx) error", shaped: ctxErrShaped, implements: implementsAs[reloader]},
	{name: "PageMeta", want: "func() via.Meta", shaped: metaShaped, implements: implementsAs[pageMetaer], rootOnly: true},
}

// hookAliases maps a plausible mis-spelling to the hook it was surely meant to
// be. Only consulted for methods that also have the hook signature, which is
// what keeps an ordinary action handler (func(*Ctx), no return) out of it.
var hookAliases = map[string]string{
	"Init": "OnInit", "Initialize": "OnInit", "Initialise": "OnInit",
	"OnInitialize": "OnInit", "OnInitialise": "OnInit", "OnStart": "OnInit",
	"Reload": "OnReload", "OnReloaded": "OnReload", "Refresh": "OnReload",
	"OnRefresh": "OnReload", "Reinit": "OnReload", "OnReInit": "OnReload",
	"Meta": "PageMeta", "Metadata": "PageMeta", "PageMetadata": "PageMeta",
	"GetPageMeta": "PageMeta", "DocumentMeta": "PageMeta", "PageInfo": "PageMeta",
	"Connect": "OnInit",
}

// aliasFor resolves a method name to the hook it was surely meant to be. An
// alias matches case-insensitively; a hook name also matches one slip away
// ("OnRelaod", "Oninit"). The slip tolerance is not extended to the aliases:
// their one-edit neighbours include real names ("Preload" next to "Reload",
// "OnInitialized" next to "OnInitialize").
func aliasFor(method string) (string, bool) {
	lower := strings.ToLower(method)
	for alias, hook := range hookAliases {
		if lower == strings.ToLower(alias) {
			return hook, true
		}
	}
	for _, h := range hookSpecs {
		if oneSlip(lower, strings.ToLower(h.name)) {
			return h.name, true
		}
	}
	return "", false
}

// v07OnConnect reports whether a method name is the v0.7 OnConnect hook or a
// slip of it. It is checked apart from the aliases because it warns even next
// to an OnInit: v0.7 code had both, and v0.8 calls neither the method nor
// anything in its place.
func v07OnConnect(method string) bool {
	return oneSlip(strings.ToLower(method), "onconnect")
}

// oneSlip reports whether a is b or one typing slip from it: an insertion, a
// deletion, a substitution, or two adjacent letters swapped (optimal string
// alignment distance <= 1). Two slips are too many: "OnRelay", "OnInput" and
// "Preload" are two from a hook name and are ordinary methods.
func oneSlip(a, b string) bool {
	if len(a) < len(b) {
		a, b = b, a
	}
	if len(a)-len(b) > 1 {
		return false
	}
	i := 0
	for i < len(b) && a[i] == b[i] {
		i++
	}
	switch {
	case i == len(b):
		return true
	case len(a) != len(b):
		return a[i+1:] == b[i:]
	}
	return a[i+1:] == b[i+1:] || swappedAt(a, b, i)
}

// swappedAt reports whether same-length a and b differ only by the two letters
// at i and i+1 trading places.
func swappedAt(a, b string, i int) bool {
	return i+1 < len(a) && a[i] == b[i+1] && a[i+1] == b[i] && a[i+2:] == b[i+2:]
}

func hookByName(name string) hookSpec {
	for _, h := range hookSpecs {
		if h.name == name {
			return h
		}
	}
	panic("via: no hook named " + name)
}

func implementsAs[T any](pt reflect.Type) bool {
	return pt.Implements(reflect.TypeOf((*T)(nil)).Elem())
}

var hookSigChecked sync.Map // reflect.Type -> true (only on a clean pass)

// checkHooks catches the ways a composition can miss a hook it meant to
// implement. A method literally named OnInit/OnReload/PageMeta with the wrong
// signature is unambiguous, so it panics here at Mount/Child rather than
// serving forever with the hook dead. A near-miss name is a heuristic, so it
// only warns — but only when the method carries the exact hook signature and
// the real interface is unsatisfied, which is a shape nothing but the mistake
// produces. A leftover string-returning Title, the hook PageMeta replaced,
// also only warns.
//
// root says whether t is being mounted. Only the root's PageMeta is read, so a
// correctly-shaped one on a child is reported: it is a warning and
// not a panic because the very same type may legitimately be a mounted page
// elsewhere in the app, and panicking would outlaw that.
func checkHooks(log *slog.Logger, t reflect.Type, warned *sync.Map, root bool) {
	if t == nil {
		return
	}
	pt := reflect.PointerTo(t)
	panicOnMisshapedHooks(t, pt)
	if _, dup := warned.LoadOrStore(t, true); dup {
		return
	}
	if !root {
		warnRootOnlyHooksOnChild(log, t, pt)
	}
	warnLeftoverTitle(log, t, pt)
	warnMisnamedMethods(log, t, pt)
}

// panicOnMisshapedHooks panics on a method named like a hook but with the
// wrong signature. Only a clean pass is memoized, so a type that panics
// panics every time.
func panicOnMisshapedHooks(t, pt reflect.Type) {
	if _, done := hookSigChecked.Load(t); done {
		return
	}
	for _, hook := range hookSpecs {
		if m, ok := pt.MethodByName(hook.name); ok && !hook.shaped(m.Type) {
			panic(render.Miswired("via: " + t.String() + "." + hook.name + " has signature " +
				withoutReceiver(m.Type) + ", not " + hook.want +
				" — so the hook will never run"))
		}
	}
	hookSigChecked.Store(t, true)
}

func warnRootOnlyHooksOnChild(log *slog.Logger, t, pt reflect.Type) {
	for _, hook := range hookSpecs {
		if hook.rootOnly && hook.implements(pt) {
			log.Warn(fmt.Sprintf("via: %s.%s is ignored — only the mounted root's %s names the document, "+
				"and %s is a child. Move it to the mounted root, or fold the value into the "+
				"root's own %s.", t.String(), hook.name, hook.name, t.String(), hook.name))
		}
	}
}

// warnLeftoverTitle: Title was the hook PageMeta replaced. A leftover one is
// dead code that still compiles and still looks like it names the page, so
// say so.
func warnLeftoverTitle(log *slog.Logger, t, pt reflect.Type) {
	if m, ok := pt.MethodByName("Title"); ok && stringShaped(m.Type) && !implementsAs[pageMetaer](pt) {
		log.Warn(fmt.Sprintf("via: %s.Title is no longer a via hook — the document is named by "+
			"PageMeta() via.Meta now, so nothing will ever call it. Return via.Meta{Title: …} instead.",
			t.String()))
	}
}

// warnMisnamedMethods warns on a method with a hook's exact signature under a
// name via never calls: the v0.7 OnConnect, or a near-miss of a current hook.
func warnMisnamedMethods(log *slog.Logger, t, pt reflect.Type) {
	for i := range pt.NumMethod() {
		m := pt.Method(i)
		if v07OnConnect(m.Name) && ctxErrShaped(m.Type) {
			log.Warn(fmt.Sprintf("via: %s.%s is shaped like the v0.7 OnConnect hook, which v0.8 never calls. "+
				"Move its work into OnInit: ctx.OnConnect(fn) runs fn once when the stream opens, and "+
				"ctx.Tick or ctx.Listen makes the unit live.", t.String(), m.Name))
			continue
		}
		name, aliased := aliasFor(m.Name)
		if !aliased {
			continue
		}
		hook := hookByName(name)
		if !hook.shaped(m.Type) || hook.implements(pt) {
			continue
		}
		log.Warn(fmt.Sprintf("via: %s.%s looks like a mis-named %s — it has the hook's exact signature "+
			"but %s has no %s %s, so nothing will ever call it. Rename it to %s.",
			t.String(), m.Name, hook.name, t.String(), hook.name, hook.want, hook.name))
	}
}

// ctxErrShaped reports whether mt is func(*via.Ctx) error; NumIn() == 2
// because the receiver still occupies In(0) for a method's reflect.Type.
func ctxErrShaped(mt reflect.Type) bool {
	return mt.NumIn() == 2 && mt.In(1) == reflect.TypeOf((*Ctx)(nil)) &&
		mt.NumOut() == 1 && mt.Out(0) == reflect.TypeOf((*error)(nil)).Elem() &&
		!mt.IsVariadic()
}

// stringShaped reports whether a method type is func() string — the retired
// Title hook's shape, kept only to recognise a leftover one.
func stringShaped(mt reflect.Type) bool {
	return mt.NumIn() == 1 && mt.NumOut() == 1 && mt.Out(0) == reflect.TypeOf("") &&
		!mt.IsVariadic()
}

func metaShaped(mt reflect.Type) bool {
	return mt.NumIn() == 1 && mt.NumOut() == 1 && mt.Out(0) == reflect.TypeOf(Meta{}) &&
		!mt.IsVariadic()
}

// typeName names the composition behind a root pointer for a panic message,
// without reflect: every root reaches here as *T from Mount's own PT.
func withoutReceiver(mt reflect.Type) string {
	in := make([]string, 0, mt.NumIn()-1)
	for i := 1; i < mt.NumIn(); i++ {
		in = append(in, mt.In(i).String())
	}
	out := make([]string, 0, mt.NumOut())
	for i := range mt.NumOut() {
		out = append(out, mt.Out(i).String())
	}
	s := "func(" + strings.Join(in, ", ") + ")"
	switch len(out) {
	case 0:
	case 1:
		s += " " + out[0]
	default:
		s += " (" + strings.Join(out, ", ") + ")"
	}
	return s
}
