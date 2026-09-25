package demos

// No card shows this file. It wipes the state visitors share, so one visitor's
// mess does not greet the next; demo.Reset runs it on a timer.

// ResetAll clears every store the demos share between visitors.
func ResetAll() {
	resetShared()
	resetVotes()
	resetStart()
	resetLanding()
}
