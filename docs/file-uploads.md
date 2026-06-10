---
title: File uploads
layout: default
parent: Guides
nav_order: 5
---

# File uploads

Add a `via.File` field. The action dispatcher detects multipart bodies and
binds the named part for the duration of the action:

```go
type Page struct {
    Avatar via.File           `via:"avatar"`
    Note   via.Signal[string] `via:"note"`
}

func (p *Page) Upload(ctx *via.Ctx) error {
    if !p.Avatar.Present() {
        return nil
    }
    // Never build a path from the client-supplied Filename(). Generate your
    // own collision-resistant name and keep only the (validated) extension.
    // newID() is yours — e.g. a DB key or crypto/rand token.
    dst := filepath.Join("/var/uploads", newID()+filepath.Ext(p.Avatar.Filename()))
    return p.Avatar.Save(dst)
}
```

The handle exposes:

- `Present()` — whether a part was uploaded for this field.
- `Filename()` — client-supplied name (**untrusted** — never use as a path).
- `Size()` — part body size in bytes.
- `ContentType()` — client-claimed type (**untrusted**).
- `Open()` — `multipart.File` stream; caller closes.
- `Bytes()` — read the whole body into memory.
- `Save(path)` — stream to disk, mode `0o600`, truncate. Use a path you
  generated, never the client `Filename()`, to avoid path traversal.

Text fields in the same multipart POST populate `Signal[T]` fields just like
a JSON action body.

## Raw streaming control

For mixed parts, custom headers, or files larger than the in-memory buffer,
call `ctx.MultipartReader()`:

```go
mr, err := ctx.MultipartReader()
```

Once read, typed `via.File` fields on the same action will be empty for any
parts already advanced past.

{: .note }
Two separate caps apply. `WithMaxRequestBody(n)` caps plain action POST and
SSE-close bodies (default 1 MiB); `WithMaxUploadSize(n)` caps
`multipart/form-data` bodies (default 32 MiB), since file parts inflate the
body well past the JSON cap. Either overflow returns `413 Too Large`.
Customise that response with `WithRequestTooLarge(h)`.

See `internal/examples/upload` for a `<form>`-driven upload persisted to
disk with a redirect-back-to-`/`.
