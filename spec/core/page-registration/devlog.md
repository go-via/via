---
feature: page-registration
status: draft
created: 2026-03-10
---

## 2026-03-10 — Spec inferred from codebase

Scoped area: routing

Working set:
- via_test.go [Depth 2 — behavioral evidence]
- via.go [Depth 2 — actual behavior]
- context.go [Depth 2 — actual behavior]
- spec/_system/map.md [Depth 1 — context]
- internal/examples/pathparams/main.go [Depth 2 — usage example]

Conflicts surfaced: None — test and code are consistent

Key resolutions:
- B3 simplified to remove implementation details ("panics with nil viewfn" → "cannot be registered")

Untested behaviors captured:
- B2 has no behavioral test — only demonstrated in example code. Marked as confidence: medium.
