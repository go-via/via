package via

import "time"

// SetMaxSSEConnForTest overrides maxSSEConn for the life of the test binary
// only — there is no public way to change the cap; a test that needs a small
// cap (to avoid opening thousands of real connections) restores it via the
// returned func.
func SetMaxSSEConnForTest(n int) (restore func()) {
	prev := maxSSEConn
	maxSSEConn = n
	return func() { maxSSEConn = prev }
}

// SetPinnedDeadlineForTest shortens the wait before a dispatch declares the
// child goroutine pinned, so a test need not block for the production value.
func SetPinnedDeadlineForTest(d time.Duration) (restore func()) {
	prev := pinnedDeadline
	pinnedDeadline = d
	return func() { pinnedDeadline = prev }
}
