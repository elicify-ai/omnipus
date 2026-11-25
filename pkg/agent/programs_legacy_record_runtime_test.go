package agent

// Spec rev8 S144 / FR-050 / I-2 (R5-M1): stored records written WITHOUT
// provenance_mode (a record from before the feature), woken by a real web turn
// in the real loop. Such a record cannot come from a producer that stamps the
// mode, so each is written through the real session and lifecycle stores with
// the mode left empty; the classification table decides on load.
// Oracle (spec S144): the old parentless task session and the record with no
// origin are lineage/unresolved and print 0; the old main session is root and
// a web turn in it prints 16.
import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// programsLegacyRecord writes a lifecycle record the way a pre-feature build
// did: no provenance_mode, no stored provenance.
func programsLegacyRecord(t *testing.T, f *wu5Fixture, id string, origin *session.Origin) {
	t.Helper()
	require.NoError(t, f.al.GetSessionLifecycleStore().Persist(&session.LifecycleRecord{
		SessionID: id, Generation: 1, State: session.LifecycleRunning, OwnerScopeKind: session.OwnerScopeHuman,
		WorkspaceID: "sales", AgentID: "dev", Origin: origin,
	}))
}

// programsLegacySession mints a parentless session of kind with workspace and
// owner stamped, as the seeded re-entry rows do, and returns its id.
func programsLegacySession(t *testing.T, f *wu5Fixture, kind session.UnifiedSessionType, taskID string) string {
	t.Helper()
	store := f.al.GetSessionStore()
	meta, err := store.NewSession(kind, "webchat", "dev")
	require.NoError(t, err)
	workspaceID, owner := "sales", wu5Owner
	patch := session.MetaPatch{WorkspaceID: &workspaceID, Owner: &owner}
	if taskID != "" {
		patch.TaskID = &taskID
	}
	require.NoError(t, store.SetMeta(meta.ID, patch))
	return meta.ID
}

func TestProgramProvenance_LegacyRecordsRuntime(t *testing.T) {
	t.Run("S144_parentless_task_record_without_provenance_mode_fails_closed", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		programsWireMessaging(t, f)
		control, _ := programsCountInput(t, f, f.mainSession("sales", "dev"), "s144-task-control", programsWebInput())
		require.Equal(t, "16", control, "instrument: the connected identity receives the token on a trusted web turn in a root")
		const taskID = "s144-legacy-task"
		sid := programsLegacySession(t, f, session.SessionTypeTask, taskID)
		programsLegacyRecord(t, f, sid, &session.Origin{Kind: session.OriginKindTask, TaskID: taskID})
		count, result := programsCountInput(t, f, sid, "s144-legacy-task", programsWebInput())
		assert.Equal(t, "0", count, "S144: the old parentless task session is lineage/unresolved and prints 0")
		assert.Contains(t, result, programsUnresolvedNotice, "S144: no stored provenance is unresolved, never a permissive fallback")
	})

	t.Run("S144_main_record_without_provenance_mode_is_root_and_web_turn_prints_16", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		programsWireMessaging(t, f)
		sid := f.mainSession("sales", "dev")
		programsLegacyRecord(t, f, sid, &session.Origin{Kind: session.OriginKindMain})
		count, result := programsCountInput(t, f, sid, "s144-legacy-main", programsWebInput())
		assert.Equal(t, "16", count, "S144: the old main session is root, so a web turn in it reaches the token")
		assert.NotContains(t, result, "Connected programs were not used for this command:")
		record, err := f.al.GetSessionLifecycleStore().Load(sid)
		require.NoError(t, err)
		assert.Empty(t, record.Provenance, "I-2: a root stores no accumulated provenance")
	})

	t.Run("S144_parentless_record_with_missing_origin_fails_closed", func(t *testing.T) {
		f := newProgramsTurnFixture(t)
		programsWireMessaging(t, f)
		control, _ := programsCountInput(t, f, f.mainSession("sales", "dev"), "s144-origin-control", programsWebInput())
		require.Equal(t, "16", control, "instrument: the connected identity receives the token on a trusted web turn in a root")
		sid := programsLegacySession(t, f, session.SessionTypeChat, "")
		programsLegacyRecord(t, f, sid, nil)
		count, result := programsCountInput(t, f, sid, "s144-legacy-no-origin", programsWebInput())
		assert.Equal(t, "0", count, "S144: unknown identity fails closed: lineage/unresolved prints 0")
		assert.Contains(t, result, programsUnresolvedNotice, "S144: unresolved, never a permissive fallback")
	})
}
