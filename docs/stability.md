---
title: API stability
layout: default
parent: Reference & ops
nav_order: 6
---

# API stability (pre-1.0)

{: .no_toc }

Via is pre-1.0: APIs can still shift. This page is the contract for what is
stable, what is experimental, and — because Via binds state by struct field and
routes actions by method name — what counts as a breaking change.

1. TOC
{:toc}

## Two tiers

**Stable core.** The surface you build every app on. It will be frozen at 1.0
and changed only under semver-major after:

- the four reactive handles and their methods — `Signal[T]`, `StateTab[T]`,
  `StateSess[T]`, `StateApp[T]` (`Read` / `Write` / `Update` / `Text` / …);
- the typed `Num` / `Bool` / `Str` / `Slice` / `Map` wrappers and their
  `Op(ctx)` verbs;
- the `on.*` event helpers and the `h` HTML builders;
- `via.New`, `via.Mount`, `App.Group`, `App.Use`, and the non-experimental
  `WithX` options;
- the composition model: a struct with a `View`, `OnInit` / `OnConnect` /
  `OnDispose` hooks, and action methods.

**Experimental.** Younger surface that may change before 1.0. Each is marked
`EXPERIMENTAL:` in its godoc:

- the state backplane — the `Backplane` interface and `WithBackplane`. The
  default single-pod (`InMemory`) behavior is stable; the clustered/distributed
  path is pre-GA and **1.0 does not promise a distributed GA**;
- cross-pod broadcast — `Broadcast`, `BroadcastNotify`, `BroadcastSignals`
  (single-process behavior is stable; cross-pod rides the backplane);
- the plugin system — the `Plugin` interface and the bundled `picocss` /
  `echarts` / `maplibre` packages;
- the notification surface — `Ctx.Notify` (the contract is stable; the rendered
  toast markup/styling is not);
- young convenience helpers — `Signal.TextSpan`, `Signal.ShowUnless`,
  `LocalSignal.ShowUnless`;
- diagnostic knobs — `WithStrictDecode`, `WithVerboseErrors`,
  `WithoutDevChecks`.

## What counts as a breaking change

Via resolves state by **reflection over your struct fields** and dispatches
actions by **method name**. Two consequences for the wire protocol:

- A composition's **exported field names and their order**, and the **wire key**
  each resolves to, are part of the contract. Renaming a state field (or
  changing the `via:"name"` tag) changes the signal key the browser binds to —
  a breaking change for any live tab mid-session.
- A **root action method's name** is its wire identifier (`on.Click(p.Save)`
  routes to `"Save"`). Renaming it breaks every rendered button that targets it.

So at 1.0 these are semver-load-bearing: **field names/order + tags, and root
method names**. Internal field layout you don't export, and anything unexported,
is never part of the contract.

## The evolution seam

The **URL route is where you evolve without breaking in place.** A new page
shape ships at a new route (or a versioned one) and the old route keeps serving
the old contract until you retire it — the same way you'd version any HTTP
endpoint. Rename a Go field without moving its wire key by pinning the key with
a `via:"name"` tag. Add fields and methods freely; that is additive. Remove or
rename exported state/actions only across a route (or major) boundary.

## Enforcing the contract

This contract is no longer review-hope: it is enforced mechanically in CI.

`ci-check.sh` runs [`apidiff`](https://pkg.go.dev/golang.org/x/exp/cmd/apidiff)
against a committed golden baseline of the public, importable packages —
`github.com/go-via/via` and its `/h`, `/on`, `/sess`, `/mw` subpackages — stored
under `api/`. Any **incompatible** change to an exported symbol (a removed or
renamed func, a changed signature, a narrowed type) fails the build. **Additive**
changes (new exported funcs, fields, methods) are compatible and never fail.

When you **intentionally** change the public API, regenerate and commit the
baseline:

```sh
./api/regen.sh
git add api/
```

The baseline files are gob-encoded binary (apidiff's on-disk format), so the
*readable* review artifact is the accompanying source change to the exported
symbol; a baseline file changing in the PR is the signal that the public API
moved and the gate was deliberately re-baselined.

The gate is a **"no silent public break" detector**, not a tier enforcer:
apidiff works at whole-package granularity and cannot tell a stable-Core symbol
from an `EXPERIMENTAL:` one. So the Core-vs-experimental distinction is enforced
**in review** against this page:

- An incompatible change to a **stable Core** symbol must be rejected (or
  deferred to a semver-major / new route).
- An incompatible change to an **EXPERIMENTAL** symbol is permitted pre-1.0 and
  is landed by regenerating the baseline, reviewed in the diff.
