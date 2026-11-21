//go:build darwin || linux

package gateway

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/tests/officepreview"
	"github.com/stretchr/testify/require"
)

// W2-S02 positive controls: real gateway wiring, not a snapshot constructed by
// this test. The previous parent functions remain owned by this same author.
func TestWriteExclusionUsesFreshConfigAndAllSessions(t *testing.T) {
	t.Run("config_pointer_replaced", TestOfficeWriterExclusion_GatewaySeesConfigAddedAfterBoot)
	t.Run("other_session_write_grant", TestOfficeWriterExclusion_GatewaySeesOtherSessionGrant)
	t.Run("read_only_grant_does_not_disable", func(t *testing.T) {
		api, cleanup := newTestRestAPI(t)
		t.Cleanup(cleanup)
		t.Setenv("OMNIPUS_HOME", api.homePath)
		root := filepath.Join(t.TempDir(), "office")
		target := officepreview.TrustedOffice(t, api.homePath, root)
		svc := officeSecurityBoot(t, api)
		require.Equal(t, "ready", svc.ComponentStatus().Office)
		grants := api.agentLoop.ApprovalGrants()
		require.NotNil(t, grants)
		ctx := tools.WithReadConfined(tools.WithTranscriptSessionID(tools.WithAgentID(context.Background(), "mia"), "read-only-other-session"), true)
		work := t.TempDir()
		resolved, err := filepath.EvalSymlinks(target)
		require.NoError(t, err)
		before, err := tools.ResolveTurnFSPolicy(ctx, work, true)
		require.NoError(t, err)
		// Use the actual bash preflight predicate with its explicit
		// read-confined input. With no installed kernel sandbox, cat is on
		// the documented read-only bypass and never reaches D7, so that
		// shell command cannot serve as a grant-consumption control.
		denied := tools.EvaluateFSPreflight(before, resolved, fspolicy.PathGrantAccessRead, true)
		require.False(t, denied.Refused)
		require.False(t, denied.Contained, "without the grant a confined read requires widening")
		require.True(t, grants.RecordPathGrant("read-only-other-session", "mia", fspolicy.PathGrant{Path: resolved, Access: fspolicy.PathGrantAccessRead}))
		all := grants.AllPathGrants()
		require.Equal(t, []fspolicy.PathGrant{{Path: resolved, Access: fspolicy.PathGrantAccessRead}}, all)
		policy, err := tools.ResolveTurnFSPolicy(ctx, work, true, grants.PathGrantsFor("read-only-other-session", "mia"))
		require.NoError(t, err)
		read := tools.EvaluateFSPreflight(policy, resolved, fspolicy.PathGrantAccessRead, true)
		require.False(t, read.Refused)
		require.True(t, read.Contained, "the real preflight must consume the recorded read authority")
		write := tools.EvaluateFSPreflight(policy, resolved, fspolicy.PathGrantAccessWrite, true)
		require.False(t, write.Refused)
		require.False(t, write.Contained, "the read grant must not authorize a write")
		require.Equal(t, "ready", svc.ComponentStatus().Office, "read-only authority is not a component writer")
	})
}
