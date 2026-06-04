package backplanetest_test

import (
	"testing"

	"github.com/go-via/via"
	"github.com/go-via/via/backplanetest"
)

// via.InMemory() is the base in-process Backplane every single-pod run uses, so
// it must satisfy the same contract as any network backend. Running the shared
// conformance suite against it both validates InMemory and exercises the suite
// itself (so a broken assertion surfaces here, not only against a real backend).
func TestInMemoryConformance(t *testing.T) {
	t.Parallel()
	backplanetest.RunConformance(t, func() via.Backplane { return via.InMemory() })
}
