// probe_timeout_test.go — the managed binary's `--version` probe gets a
// realistic budget (2026-08-13, macOS): a freshly-downloaded ~200MB Chrome
// bundle pays Gatekeeper's whole-bundle signature verification on its FIRST
// execution, which exceeded the 5s PATH-candidate timeout and made a healthy
// install report itself corrupt ("remove and retry"). Measured on a 4-core
// Intel MacBook Pro: first probe >5s, immediately-following probe <1s.

package browser

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestManagedProbeTimeout_IsFarLongerThanPathCandidateTimeout(t *testing.T) {
	require.Greater(t, managedChromiumProbeTimeout, chromiumProbeTimeout,
		"the single no-fallback managed binary must get a longer budget than one of several PATH candidates")
	require.GreaterOrEqual(t, managedChromiumProbeTimeout, 60*time.Second,
		"must comfortably cover macOS Gatekeeper verifying a ~200MB bundle on first exec")
}
