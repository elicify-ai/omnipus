package agent

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Oracle: NEW-2 — Launch returns the classification it PERSISTED, so a caller
// that projects the child's runtime (delegate's is_3p) never re-resolves it.
// Real: SteerLauncher.Launch against a real lifecycle store; the expected value
// is the executor kind configured for the target in the fixture config.
func TestLaunchResult_Is3PMatchesThePersistedRecord(t *testing.T) {
	// The Launch gate consults the default workspace's delegation graph: the
	// steering agent may delegate to both targets.
	seedWorkspaceGraph(t, "01JWLAUNCHIS3P0000000001", true, []graphEdge{
		edge(testDefaultAgentID, delegateExtCLIAgentID, nil, nil),
		edge(testDefaultAgentID, delegateNativeWorkerAgentID, nil, nil),
	})
	al := newDelegateDispatchLoop(t, &delegateDispatchProvider{})
	for _, tc := range []struct {
		name   string
		target string
		want   bool
	}{
		{"external-cli target", delegateExtCLIAgentID, true},
		{"native target", delegateNativeWorkerAgentID, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			res := launchDelegateTo(t, al, tc.target, "classification proof")
			rec, err := al.GetSessionLifecycleStore().Load(res.SessionID)
			require.NoError(t, err)
			require.Equal(t, tc.want, rec.Is3P, "fixture sanity: the persisted record carries the configured kind")
			require.Equal(t, rec.Is3P, res.Is3P, "LaunchResult.Is3P must be the persisted classification")
		})
	}
}
