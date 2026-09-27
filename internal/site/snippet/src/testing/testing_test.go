package testing_test

import (
	"testing"

	samples "go-via.dev/site/snippet/src/testing"
)

func TestCounter_incrementsOnClick(t *testing.T)     { samples.TestCounter_incrementsOnClick(t) }
func TestStepper_addsThePostedStep(t *testing.T)     { samples.TestStepper_addsThePostedStep(t) }
func TestShelf_eachRowCarriesItsOwnArg(t *testing.T) { samples.TestShelf_eachRowCarriesItsOwnArg(t) }
func TestLayout_routesTheChildActionToSearch(t *testing.T) {
	samples.TestLayout_routesTheChildActionToSearch(t)
}
func TestCounter_refusesCrossSitePosts(t *testing.T) { samples.TestCounter_refusesCrossSitePosts(t) }
func TestCounter_overTLSRefusesAnHTTPOrigin(t *testing.T) {
	samples.TestCounter_overTLSRefusesAnHTTPOrigin(t)
}
func TestBoard_pushesOverTheStream(t *testing.T) { samples.TestBoard_pushesOverTheStream(t) }
func TestClock_ticksOncePerMinute(t *testing.T)  { samples.TestClock_ticksOncePerMinute(t) }
func TestBoard_shutdownEndsTheStreamCleanly(t *testing.T) {
	samples.TestBoard_shutdownEndsTheStreamCleanly(t)
}
func TestRoom_aPublishReachesEveryTab(t *testing.T) { samples.TestRoom_aPublishReachesEveryTab(t) }
