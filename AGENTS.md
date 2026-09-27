# MUST FOLLOW AT ALL TIMES

- When exploring the codebase or reading any file, read
  [CONVENTIONS.md](./CONVENTIONS.md) integrally. Adherence to it is mandatory.
- Do not commit without explicit user approval. Ask the user before
  committing.

## When a property becomes a duck-typed method

via's duck-typed surface is deliberately tiny — `View`, `OnInit`, `OnReload`,
`PageMeta` — in two families: lifecycle phases, and document queries. Every
addition costs every reader a name they must know is magic, so a new one has
to clear all three of these:

1. It is a pure function of the type's own data (no arguments, no side
   effects, safe to call again).
2. It writes a slot shared by more than one unit, so an imperative setter
   would let any unit in the tree clobber it.
3. It is read exactly once per document render.

`PageMeta` clears all three, which is why it is a method on the root and not
`ctx.Title(…)`: the document head is one shared slot, and the setter shape let
any embedded child silently rename the page.

Measured against the same rule and deliberately left imperative:

- `ctx.Tick` / `ctx.Listen` — per-unit registrations with no shared slot to
  clobber, and they are side effects, not queries (fails 1 and 2).
- `ctx.Redirect` — conditional, and not read once per render (fails 1 and 3).
- The `With*` options — router-wide policy: cookie policy, body caps and the
  CSP floor every mount shares. A page must not be able to change them, so
  they stay outside the composition entirely. A page widens its own CSP only
  through `Meta.Assets`.

There is deliberately no `Description` or `Assets` sibling: `PageMeta` is the
one document-query hook and every new head slot is a field on `via.Meta`, not a
fifth method. `Meta.Assets` is the exception to rule 3: it decides the mount's
CSP, so via reads it three times — at `Mount` off the mounted literal, at
`Mount` off a probe copy with its zero fields filled in, and on every document
render — and panics if any two disagree. If `PageMeta` panics on the probe's
synthetic data, via logs a WARN and skips the probe, leaving the render-time
check to catch drift on the first request. Anything CSP-relevant must follow
that shape: declared once, off the mounted literal, never derived from a
request.
