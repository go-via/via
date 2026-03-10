# Glossary

Aggregated from all specs and CONVENTIONS.md. Auto-maintained by the agent.
Last rebuilt: 2026-03-10

| Term | Definition | Source |
|---|---|---|
| Session | A user interaction with a page | spec:session-management |
| Session creation | The beginning of a session when a user visits a page | spec:session-management |
| Session cleanup | The end of a session when a user navigates away or closes the browser | spec:session-management |
| Session ID | Unique identifier for a session | spec:session-management (conflict term) |
| Page | A URL route connected to a view function that renders HTML | spec:page-registration |
| Route | A URL pattern that maps to a page handler | spec:page-registration |
| Route parameters | Named parameters extracted from route patterns (e.g., `/users/{id}`) | spec:page-registration |
| View | A function that renders HTML in response to a request | spec:page-registration |
| Behavioral name | A name describing the action or transformation, not the module | CONVENTIONS.md |
| Signal | A reactive value that syncs between server and client | spec:_system:map.md |
| Action | An event binding that triggers server-side handlers | spec:_system:map.md |
