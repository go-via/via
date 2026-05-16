package via

// Sentinel-error intents recognised by the action dispatcher. A helper
// deep in an action's call chain can return one of these instead of
// `do-the-side-effect-then-return-nil`, expressing the post-action
// outcome as the function's return value:
//
//	func (p *LoginForm) Submit(ctx *via.Ctx) error {
//	    if err := authenticate(...); err != nil {
//	        return err            // → action-error handler (default: toast)
//	    }
//	    return via.Redirect("/dashboard")  // → tab navigates
//	}
//
// The dispatcher unwraps the error via errors.As; the same sentinels
// survive fmt.Errorf("%w", …) wrapping. Outside an action dispatcher
// they behave like any other error.

// RedirectError is returned by [Redirect] and recognised by the action
// dispatcher as an intent to navigate the tab rather than report a
// failure. The dispatcher unwraps it via errors.As, calls ctx.Redirect
// with URL, and does not invoke the action-error handler.
//
// Returning a typed error (rather than calling ctx.Redirect inline and
// `return nil`) means a helper deep in the call chain can express the
// redirect intent without threading *Ctx through every frame.
type RedirectError struct{ URL string }

func (e *RedirectError) Error() string { return "via: redirect to " + e.URL }

// Redirect returns an error that the action dispatcher translates into
// a client-side navigation to url. Idiomatic for "do work, then go
// somewhere" actions:
//
//	func (p *LoginForm) Submit(ctx *via.Ctx) error {
//	    if err := authenticate(...); err != nil {
//	        return err // surfaces as a toast / custom error handler
//	    }
//	    return via.Redirect("/dashboard")
//	}
//
// Outside an action dispatcher the value behaves like any other error;
// callers may inspect it via errors.As(err, &*RedirectError).
func Redirect(url string) error { return &RedirectError{URL: url} }

// ToastError is returned by [Toast] and recognised by the action
// dispatcher as an intent to show a browser alert rather than report
// a failure. The dispatcher unwraps it via errors.As, calls
// ctx.Toast(Message), and does not invoke the action-error handler.
type ToastError struct{ Message string }

func (e *ToastError) Error() string { return "via: toast " + e.Message }

// Toast returns an error that the action dispatcher translates into a
// JSON-safe browser alert containing msg. Use for "report a result and
// stop" flows where the message itself is the success outcome:
//
//	func (p *Form) Save(ctx *via.Ctx) error {
//	    if err := store(...); err != nil { return err }
//	    return via.Toast("saved!")
//	}
//
// Empty msg is a no-op (still treated as a successful return; nothing
// is pushed to the client). For richer notifications, prefer
// PatchSignal driving a client-side notice signal.
func Toast(msg string) error { return &ToastError{Message: msg} }
