---
feature: session-management
status: draft
created: 2026-03-10
---

## 2026-03-10 — Spec inferred from codebase

Scoped area: session-management

Working set:
- via.go [Depth 2 — actual behavior]
- context.go [Depth 2 — actual behavior]
- spec/_system/map.md [Depth 1 — context]

Key resolutions:
- Split from "routing" area as per user direction
- B1-B3: Purely behavioral descriptions, no implementation details
- B4 (active session count) removed as unnecessary for core behavior
- "browser tab" refined to "multiple sessions" to avoid technical term

Conflicts surfaced:
- Code uses: map-based registry, POST /_session/close endpoint, auto-generated ID
- Spec uses: "registry", "user leaves", no ID mechanism specified

Untested behaviors captured:
- All session behaviors (B1-B3) lack behavioral tests — marked confidence: high based on code only
