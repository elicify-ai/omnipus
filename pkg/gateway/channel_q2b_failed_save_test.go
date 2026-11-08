package gateway

import (
	"encoding/json"
	"net/http"
	"os"
	"sync"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A1 / CHECK F1: a real writer failure after approval must omit the saved-only
// verification field, preserve settings and keep every target out of the audit.
func TestChannelsQ2B_A1_PostApprovalWriterFailureOmitsTargetVerifications(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "jim")
	sid := cq2Caller(t, f, "mia", false, false)
	cq2Stored(t, f, "telegram", "reply_only", []string{"telegram:987654321", "telegram:888888888"})
	request := q7AuditAuth(t, f, sid)
	readOutbound := func() gen.ChannelOutboundRead {
		rec := request(http.MethodGet, "/api/v1/channels/telegram/outbound", "")
		require.Equal(t, http.StatusOK, rec.Code, "instrument: GET uses the real named approver's authentication")
		var body gen.ChannelOutboundRead
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
		require.NotEmpty(t, body.Revision)
		return body
	}
	original := readOutbound()
	require.Equal(t, "reply_only", string(original.Outbound.Policy))
	require.Equal(t, []string{"telegram:987654321", "telegram:888888888"}, original.Outbound.Targets)
	before := t13Before(t, f)
	path, backup := f.f1277.configPath, f.f1277.configPath+".a1-failed-save-fixture"
	poisoned := false
	var once sync.Once
	restore := func() {
		if !poisoned {
			return
		}
		once.Do(func() {
			if err := os.Remove(path); err != nil {
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			require.NoError(t, os.Rename(backup, path))
		})
	}
	// Register before cq2Start so its later cleanup drains/cancels the turn
	// before this fallback restores the writable config path on an early fail.
	t.Cleanup(restore)
	const callID = "cq2-a1-failed-after-approval"
	args := cq2Args("telegram", "agent_may_start", []string{"987654321", "777777777"})
	events := cq2Start(t, f, sid, "mia", callID, args)
	entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
	cq2RequireCardModelArguments(t, args, entry.Args)
	require.Equal(t, original.Revision, entry.Args["revision"], "instrument: valid card precedes the filesystem failure")
	require.Empty(t, cq2OutcomeRows(t, f, sid, callID), "instrument: no failed/refused/saved writer outcome exists before approval")

	// Poison only this fixture's filesystem edge AFTER preparation. Unlike
	// chmod under root, a directory cannot be read as the saved JSON file.
	// This is an honest writer-read failure, not an atomic-write/fsync probe.
	require.NoError(t, os.Rename(path, backup))
	poisoned = true
	require.NoError(t, os.Mkdir(path, 0o700))
	_, readErr := os.ReadFile(path)
	require.Error(t, readErr, "instrument: the real saved-config read genuinely fails before the person approves")
	var pathErr *os.PathError
	require.ErrorAs(t, readErr, &pathErr)
	require.Equal(t, path, pathErr.Path, "instrument: the failure is on the actual writer's configured path")

	approved := request(http.MethodPost, "/api/v1/tool-approvals/"+entry.ApprovalID, `{"action":"allow_once"}`)
	require.Equal(t, http.StatusOK, approved.Code, "instrument: the real person approved; this is not a pre-card refusal")
	q7WaitTurn(t, f, events, sid)
	cq2Failure(t, f.provider.lastToolResult(), "CONFIG_SAVE_FAILED", "")
	require.Equal(t, 1, cq2RequestCount(f, sid, callID), "instrument: exactly one real card and approval attempt")
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.True(t, info.IsDir(), "failure must not replace the config-path sentinel with saved settings")
	backupBytes, err := os.ReadFile(backup)
	require.NoError(t, err)
	require.Equal(t, before.disk, backupBytes, "failed writer must not change any original saved-config byte")
	require.Equal(t, before.live.Channels, f.al.GetConfig().Channels, "failed writer must not change live channel settings")
	restore()
	t13Unchanged(t, f, before)
	require.Equal(t, original, readOutbound(), "failure preserves the entire authenticated outbound response, including its original revision")

	rows := q7AuditRead(t, f, sid, "set_channel_outbound", true)
	q7AuditPrivateArguments(t, rows, "set_channel_outbound") // R-S3 helper stays byte-identical.
	for _, row := range rows {
		raw, err := json.Marshal(row)
		require.NoError(t, err)
		for _, target := range []string{"987654321", "888888888", "777777777"} {
			assert.NotContains(t, string(raw), target, "A1/R-S3: no retained, removed or proposed raw target anywhere in audit event %v", row["event"])
			assert.NotContains(t, string(raw), "telegram:"+target, "A1/R-S3: no canonical target anywhere in audit event %v", row["event"])
		}
	}
	for _, event := range []string{audit.EventToolPolicyAskRequested, audit.EventToolPolicyAskGranted} {
		consent := q7AuditEvent(t, rows, event, entry.ApprovalID)
		q7AuditIdentity(t, consent, entry, q7FollowupDigest(t, entry.Args))
		if event == audit.EventToolPolicyAskGranted {
			require.Equal(t, q7AuditApprover, consent["approver_user_id"], "instrument: actual named human granted this failed call")
		}
	}
	require.Len(t, cq2OutcomeRows(t, f, sid, callID), 1, "A1: exactly one writer outcome for the call, not a hidden refusal plus failure")
	failed := q7AuditEvent(t, rows, audit.EventChannelOutboundWrite, entry.ApprovalID)
	for key, want := range map[string]string{
		"outcome": "failed", "approval_id": entry.ApprovalID, "tool_call_id": callID,
		"session_id": sid, "agent_id": "mia", "turn_id": entry.TurnID, "channel_id": "telegram",
	} {
		require.Equal(t, want, failed[key], "A1: correlated failed writer audit field %s", key)
	}
	require.NotContains(t, failed, "target_verifications", "A1: failures omit the field entirely; null and empty lists are not absence")
}
