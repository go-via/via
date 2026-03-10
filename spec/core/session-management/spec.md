---
feature: session-management
status: draft
created: 2026-03-10
---

# Session Management

Sessions represent user interactions with pages.

## Behaviors

### B1: Session creation

**Given** a user visiting a page  
**When** the page is requested  
**Then** a session begins

**Notes:** Inferred from code: via.go:174-179. Confidence: high. No behavioral test exists.

---

### B2: Session cleanup

**Given** a user leaving a page  
**When** the user navigates away or closes the browser  
**Then** the session ends

**Notes:** Inferred from code: via.go:486-506. Confidence: high. No behavioral test exists.

---

### B3: Multiple sessions

**Given** multiple users visiting pages  
**When** each user has their own session  
**Then** sessions do not interfere with each other

**Notes:** Inferred from code: via.go:209-244. Confidence: high. No behavioral test exists.

---

## Conflicts

- Session registry: code uses a map to store sessions — behavioral description uses "registry"
- Session close: code exposes POST /_session/close endpoint — behavioral description uses "user leaves"
- Session ID: code generates unique IDs automatically — behavioral abstraction does not specify how

**No behavioral test exists for any session behavior — all marked confidence: high based on code only.**
