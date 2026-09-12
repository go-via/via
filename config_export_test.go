package via

import "sync"

// SetMaxSSEConnForTest overrides maxSSEConn for the life of the test binary
// only — there is no public way to change the cap; a test that needs a small
// cap (to avoid opening thousands of real connections) restores it via the
// returned func.
func SetMaxSSEConnForTest(n int) (restore func()) {
	prev := maxSSEConn
	maxSSEConn = n
	return func() { maxSSEConn = prev }
}

// ResetSlotFallbackWarningForTest re-arms the once-per-process warning
// signalSlot fires when a signal is not addressable inside its composition, so
// a test can observe it regardless of what ran before it.
func ResetSlotFallbackWarningForTest() { valueReceiverWarning = sync.Once{} }
