package gateway

import (
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/credentials"
	"github.com/stretchr/testify/require"
)

// #1277 must survive the runtime construction barrier too. A boot-only fix
// (return the partial bundle and ignore the error) still reaches NewManager's
// fatal factory-error aggregation. Keep the manager real, never seed failures
// with NewManagerForTesting, so this test catches that incomplete fix.
func TestIssue1277_ManagerMissingSecretIsVisibleWithoutAbortingHealthyPeer(t *testing.T) {
	f := newIssue1277Fixture(t)
	require.Empty(t, credentials.InjectFromConfig(f.cfg, f.store))
	bundle, errs := credentials.ResolveBundle(f.cfg, f.store)
	missing := 0
	for _, err := range errs {
		var notFound *credentials.NotFoundError
		if errors.As(err, &notFound) && notFound.Name == issue1277BrokenRef {
			missing++
		}
	}
	require.Equal(t, 1, missing, "instrument: real resolver must report the absent configured ref")
	api, svc, _, _ := f.startRuntime(t, f.cfg, bundle)
	issue1277RequireDegradedChannel(t, api, svc.ChannelManager)
	issue1277RequireHealthyPeer(t, f, api, svc.ChannelManager)
	issue1277RequireDoctorReason(t, api)
}
