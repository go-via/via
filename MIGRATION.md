# Migration guide

Every breaking change to Via's public API is recorded here, in the
same PR that lands the break. Each entry is an anchor heading that CI
couples to the apidiff report: a public break with no matching anchor
fails the build.

Entry format:

- A `###` heading naming the symbol(s) that changed.
- One paragraph on why the break exists.
- A before/after pair of Go snippets that both compile (the snippet
  gate compiles them like any other doc fence).

Entries are grouped newest-first under the release that ships them.

## Unreleased
