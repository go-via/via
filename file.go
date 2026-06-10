package via

import (
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"reflect"
)

// File is a typed, request-scoped handle to one uploaded file part. Add
// it as a field on a composition struct to receive a file from a
// multipart action POST:
//
//	type Page struct {
//	    Avatar via.File `via:"avatar"`
//	}
//
//	func (p *Page) Upload(ctx *via.Ctx) error {
//	    if !p.Avatar.Present() { return nil }
//	    return p.Avatar.Save("/var/uploads/" + p.Avatar.Filename())
//	}
//
// Wire key defaults to the lower-cased field name; override with the
// `via:"name"` tag exactly like Signal[T].
//
// Lifecycle: the file handle is bound at action entry from the
// multipart body and cleared when the action returns. Read, copy, or
// Save it during the action body — references are not valid afterward.
type File struct {
	header *multipart.FileHeader
	key    string
}

// Present reports whether a file part was actually uploaded for this
// field on the current action POST.
func (f *File) Present() bool { return f != nil && f.header != nil }

// Filename returns the client-supplied filename. Untrusted: never use
// it as a filesystem path without sanitizing — callers should prefer
// generating their own name and using Save with that path.
func (f *File) Filename() string {
	if f == nil || f.header == nil {
		return ""
	}
	return f.header.Filename
}

// Size returns the part body size in bytes.
func (f *File) Size() int64 {
	if f == nil || f.header == nil {
		return 0
	}
	return f.header.Size
}

// ContentType returns the Content-Type header the client sent for the
// part. Untrusted: clients can claim any content type, so use a
// content-sniffing library (net/http.DetectContentType on the first 512
// bytes) before relying on it.
func (f *File) ContentType() string {
	if f == nil || f.header == nil {
		return ""
	}
	return f.header.Header.Get("Content-Type")
}

// Open returns a stream over the file body. Caller must Close. Returns
// an error if no file was uploaded for this field.
func (f *File) Open() (multipart.File, error) {
	if f == nil || f.header == nil {
		return nil, errNoFile
	}
	return f.header.Open()
}

// Bytes reads the file body into memory and returns it. For large
// uploads prefer Open + io.Copy to avoid buffering everything at once.
func (f *File) Bytes() ([]byte, error) {
	rc, err := f.Open()
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	return io.ReadAll(rc)
}

// Save streams the file body to path with mode 0o600 (owner read/write
// only). Truncates any existing file at path. Use a path you generated
// — never the client-supplied Filename — to avoid path-traversal.
func (f *File) Save(path string) (err error) {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()
	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := out.Close(); err == nil {
			err = cerr
		}
	}()
	_, err = io.Copy(out, rc)
	return err
}

// Key returns the wire key (the multipart field name).
func (f *File) Key() string { return f.key }

var (
	errNoFile        = errors.New("via: no file uploaded for this field")
	errNoActionScope = errors.New("via: MultipartReader called outside action scope")
)

// MultipartReader returns the multipart reader on the in-flight
// request, or an error if the request body is not multipart. Use this
// from an action when you need streaming control over a multipart body
// that the typed via.File field doesn't cover (mixed parts, custom
// part headers, very large files where ParseMultipartForm's memory
// buffer is too small). Outside action scope, returns an error.
//
// Calling MultipartReader consumes the body forward-only; once read,
// typed via.File fields on the same action will be empty for any parts
// you advance past.
func (ctx *Ctx) MultipartReader() (*multipart.Reader, error) {
	r := ctx.Request()
	if r == nil {
		return nil, errNoActionScope
	}
	return r.MultipartReader()
}

// Files is a typed, request-scoped handle to ALL parts of a multi-file
// upload field (an <input type=file multiple>). Use it where via.File —
// which binds only the first part — would silently drop the rest:
//
//	type Page struct {
//	    Photos via.Files `via:"photos"`
//	}
//
//	func (p *Page) Upload(ctx *via.Ctx) error {
//	    for i, f := range p.Photos.All() {
//	        if err := f.Save(fmt.Sprintf("/uploads/%d", i)); err != nil { return err }
//	    }
//	    return nil
//	}
//
// Same lifecycle and wire-key rules as [File].
type Files struct {
	headers []*multipart.FileHeader
	key     string
}

// Len reports how many parts were uploaded for this field.
func (fs *Files) Len() int {
	if fs == nil {
		return 0
	}
	return len(fs.headers)
}

// All returns a [File] handle for each uploaded part, in form order.
func (fs *Files) All() []File {
	if fs == nil {
		return nil
	}
	out := make([]File, len(fs.headers))
	for i, h := range fs.headers {
		out[i] = File{header: h, key: fs.key}
	}
	return out
}

// Key returns the wire key (the multipart field name).
func (fs *Files) Key() string { return fs.key }

// fileSlot records the location of a via.File / via.Files field in a
// composition so the action dispatcher can populate it from a parsed
// multipart form. plural is true for via.Files (bind every part).
type fileSlot struct {
	fieldPath []int
	wireKey   string
	plural    bool
}

// isFileType reports whether t is the via.File handle.
func isFileType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return t.Name() == "File" && t.PkgPath() == viaPkgPath
}

// isFilesType reports whether t is the via.Files (multi-part) handle.
func isFilesType(t reflect.Type) bool {
	if t.Kind() != reflect.Struct {
		return false
	}
	return t.Name() == "Files" && t.PkgPath() == viaPkgPath
}

// bindFileKeys writes the wire key into every via.File field of the
// freshly allocated *C so File.Key() works at view time even before
// any multipart action has run. Mirrors bindScopeKeys.
func bindFileKeys(cmpVal reflect.Value, d *cmpDescriptor) {
	if len(d.fileSlots) == 0 {
		return
	}
	elem := cmpVal.Elem()
	for _, s := range d.fileSlots {
		field := fieldByPath(elem, s.fieldPath).Addr().Interface()
		if s.plural {
			field.(*Files).key = s.wireKey
		} else {
			field.(*File).key = s.wireKey
		}
	}
}

// bindFiles populates every via.File field on the bound composition
// from the parsed multipart form. Files not present in the form leave
// their handle empty (Present() == false). Wire keys are already set
// by bindFileKeys at Ctx construction.
func bindFiles(ctx *Ctx, form *multipart.Form) {
	if form == nil || len(ctx.desc.fileSlots) == 0 {
		return
	}
	elem := ctx.cmpReflect.Elem()
	for _, s := range ctx.desc.fileSlots {
		hs := form.File[s.wireKey]
		field := fieldByPath(elem, s.fieldPath).Addr().Interface()
		if s.plural {
			field.(*Files).headers = hs // every part; nil/empty when none uploaded
			continue
		}
		if len(hs) > 0 {
			field.(*File).header = hs[0]
		}
	}
}

// clearFiles drops every via.File header reference from the bound
// composition. Called after the action body returns so a subsequent
// non-multipart action doesn't inherit the previous file.
func clearFiles(ctx *Ctx) {
	if len(ctx.desc.fileSlots) == 0 {
		return
	}
	elem := ctx.cmpReflect.Elem()
	for _, s := range ctx.desc.fileSlots {
		field := fieldByPath(elem, s.fieldPath).Addr().Interface()
		if s.plural {
			field.(*Files).headers = nil
		} else {
			field.(*File).header = nil
		}
	}
}

// readMultipartSignals parses the multipart form on r (already
// MaxBytes-bounded by the caller) and writes text fields into the
// caller-supplied dst map as raw strings. Unlike the datastar JSON path
// (where the client chooses each signal's wire type), a multipart form is
// stringly-typed on the wire, so values are left as strings here and
// coerced to each field's target type by decodeScalarInto at inject time —
// JSON-coercing here was redundant (decodeScalarInto already parses
// string→number/bool) and lossy (it turned "007" into 7 and stopped a
// Signal[string] from holding "true"). Returns the form so file fields can
// be bound. memLimit caps how much non-file content goes through memory
// before spilling to disk — see http.Request.ParseMultipartForm. dst must
// be non-nil; pre-existing keys are preserved (caller's responsibility to clear).
func readMultipartSignals(r *http.Request, memLimit int64, dst map[string]any) (*multipart.Form, error) {
	if err := r.ParseMultipartForm(memLimit); err != nil {
		return nil, err
	}
	form := r.MultipartForm
	for k, vs := range form.Value {
		if len(vs) == 0 {
			continue
		}
		dst[k] = vs[0]
	}
	return form, nil
}
