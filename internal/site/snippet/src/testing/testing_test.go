package testing_test

import (
	"testing"

	samples "go-via.dev/site/snippet/src/testing"
)

func TestCounter_incrementsOnClick(t *testing.T) {
	// sequential: samples.TestCounter_incrementsOnClick calls t.Parallel
	samples.TestCounter_incrementsOnClick(t)
}

func TestStepper_addsThePostedStep(t *testing.T) {
	// sequential: samples.TestStepper_addsThePostedStep calls t.Parallel
	samples.TestStepper_addsThePostedStep(t)
}

func TestShelf_eachRowCarriesItsOwnArg(t *testing.T) {
	// sequential: samples.TestShelf_eachRowCarriesItsOwnArg calls t.Parallel
	samples.TestShelf_eachRowCarriesItsOwnArg(t)
}

func TestLayout_routesTheChildActionToSearch(t *testing.T) {
	// sequential: samples.TestLayout_routesTheChildActionToSearch calls t.Parallel
	samples.TestLayout_routesTheChildActionToSearch(t)
}

func TestCounter_refusesCrossSitePosts(t *testing.T) {
	// sequential: samples.TestCounter_refusesCrossSitePosts calls t.Parallel
	samples.TestCounter_refusesCrossSitePosts(t)
}

func TestCounter_overTLSRefusesAnHTTPOrigin(t *testing.T) {
	// sequential: samples.TestCounter_overTLSRefusesAnHTTPOrigin calls t.Parallel
	samples.TestCounter_overTLSRefusesAnHTTPOrigin(t)
}

func TestBoard_pushesOverTheStream(t *testing.T) {
	// sequential: samples.TestBoard_pushesOverTheStream calls t.Parallel
	samples.TestBoard_pushesOverTheStream(t)
}

func TestClock_ticksOncePerMinute(t *testing.T) {
	// sequential: samples.TestClock_ticksOncePerMinute runs wholly inside a synctest bubble
	samples.TestClock_ticksOncePerMinute(t)
}

func TestBoard_shutdownEndsTheStreamCleanly(t *testing.T) {
	// sequential: samples.TestBoard_shutdownEndsTheStreamCleanly calls t.Parallel
	samples.TestBoard_shutdownEndsTheStreamCleanly(t)
}

func TestRoom_aPublishReachesEveryTab(t *testing.T) {
	// sequential: samples.TestRoom_aPublishReachesEveryTab calls t.Parallel
	samples.TestRoom_aPublishReachesEveryTab(t)
}
