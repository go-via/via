---
feature: page-registration
status: draft
created: 2026-03-10
---

# Page Registration

A page connects a URL route to a view function that renders HTML.

## Behaviors

### B1: A page with a view renders HTML when requested

**Given** a route and a view function  
**When** a GET request matches the route  
**Then** the view is rendered as HTML

**Notes:** Inferred from test: via_test.go:14-28. Confidence: high. Yellow exemption applies.

---

### B2: A route with parameters extracts those parameters

**Given** a route pattern with named parameters like `/users/{id}`  
**When** a request matches the pattern  
**Then** the parameter values are accessible via GetPathParam

**Notes:** Inferred from code: via.go:516-531. Confidence: medium. No behavioral test exists — needs verification.

---

### B3: A page without a view cannot be registered

**Given** a page registration where View() is never called  
**When** the page is registered  
**Then** registration fails

**Notes:** Inferred from test: via_test.go:99-105. Confidence: high. Yellow exemption applies.
