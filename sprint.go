package via

import "fmt"

// sprint formats a value for internal text rendering. It uses any deliberately:
// this is internal-only and never appears on a public signature.
func sprint(v any) string { return fmt.Sprint(v) }
