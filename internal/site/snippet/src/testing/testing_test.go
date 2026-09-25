package testing_test

import (
	"testing"

	samples "go-via.dev/site/snippet/src/testing"
)

func TestCounter(t *testing.T) { samples.TestCounter(t) }

func TestStepper(t *testing.T) { samples.TestStepper_addsThePostedStep(t) }
func TestShelf(t *testing.T)   { samples.TestShelf_eachRowCarriesItsOwnArg(t) }
func TestLayout(t *testing.T)  { samples.TestLayout_searchChild(t) }
func TestOrigin(t *testing.T)  { samples.TestCounter_refusesCrossSitePosts(t) }
func TestTLS(t *testing.T)     { samples.TestCounter_overTLSRefusesAnHTTPOrigin(t) }
func TestBoard(t *testing.T)   { samples.TestBoard_pushesOverTheStream(t) }
func TestClock(t *testing.T)   { samples.TestClock_ticksOncePerMinute(t) }
func TestClose(t *testing.T)   { samples.TestBoard_shutdownEndsTheStreamCleanly(t) }
func TestRoom(t *testing.T)    { samples.TestRoom_aPublishReachesEveryTab(t) }
