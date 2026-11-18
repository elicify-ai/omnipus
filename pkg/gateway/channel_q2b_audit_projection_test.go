package gateway

import (
	"encoding/json"
	"net/http"
	"testing"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A2.1: the person sees the private lists on the card, but the ask-audit
// preview must retain readable non-target facts, not generic object summaries.
func TestChannelsQ2B_A21_AskAuditPreviewShowsReadableNonTargetFacts(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "jim")
	sid := cq2Caller(t, f, "mia", false, false)
	cq2Stored(t, f, "telegram", "reply_only", []string{
		"telegram:987654321", "telegram:888888888", "telegram:777777777",
	})
	request := q7AuditAuth(t, f, sid)
	readOutbound := func() gen.ChannelOutboundRead {
		read := request(http.MethodGet, "/api/v1/channels/telegram/outbound", "")
		require.Equal(t, http.StatusOK, read.Code, "instrument: read through the real named approver's authentication")
		var body gen.ChannelOutboundRead
		require.NoError(t, json.Unmarshal(read.Body.Bytes(), &body))
		require.NotEmpty(t, body.Revision)
		return body
	}
	beforeRevision := readOutbound().Revision
	const callID = "cq2-a21-readable-audit"
	args := cq2Args("telegram", "agent_may_start", []string{"987654321", "666666666"})
	events := cq2Start(t, f, sid, "mia", callID, args)
	entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
	require.Equal(t, "mia", entry.AgentID)
	cq2RequireCardModelArguments(t, args, entry.Args)
	// Independent fixture arithmetic: retain one, add one and remove two.
	// Different nonzero counts catch swapped, zeroed or total-count projections.
	require.Equal(t, []any{"telegram:666666666"}, entry.Args["added"], "instrument: real card contains the newly added private target")
	require.ElementsMatch(t, []any{"telegram:888888888", "telegram:777777777"}, entry.Args["removed"], "instrument: real card contains exactly both removed private targets")
	require.Equal(t, beforeRevision, entry.Args["revision"], "instrument: real card pins the original revision")
	approved := request(http.MethodPost, "/api/v1/tool-approvals/"+entry.ApprovalID, `{"action":"allow_once"}`)
	require.Equal(t, http.StatusOK, approved.Code, "instrument: real authenticated human approval route")
	q7WaitTurn(t, f, events, sid)
	wantTargets := []string{"telegram:987654321", "telegram:666666666"}
	saved := q7Saved(t, &tools.ToolResult{ForLLM: f.provider.lastToolResult()}, "agent_may_start", wantTargets)
	current := readOutbound()
	require.Equal(t, "agent_may_start", string(current.Outbound.Policy))
	require.Equal(t, wantTargets, current.Outbound.Targets, "instrument: authenticated GET returns the actual canonical saved list")
	require.Equal(t, current.Revision, saved["revision"])
	live := f.al.GetConfig().Channels["telegram"].Outbound
	require.NotNil(t, live)
	require.Equal(t, "agent_may_start", live.Policy)
	require.Equal(t, wantTargets, []string(live.Targets), "instrument: live policy actually changed")
	disk := readOutboundConfigChannelsFile(t, f)
	instance, present := disk["telegram"].(map[string]any)
	require.True(t, present)
	require.Equal(t, map[string]any{"policy": "agent_may_start", "targets": []any{"telegram:987654321", "telegram:666666666"}}, instance["outbound"], "instrument: whole canonical outbound object was durably saved")
	require.NotEqual(t, beforeRevision, current.Revision, "instrument: save advanced the revision; audit preview must not substitute the result token")
	require.Equal(t, 1, cq2RequestCount(f, sid, callID), "instrument: exactly one actual approval request")

	rows := q7AuditRead(t, f, sid, "set_channel_outbound", true)
	q7AuditPrivateArguments(t, rows, "set_channel_outbound") // R-S3 stays completely unchanged.
	// Inspect all actual audit rows, including nested and string-encoded values,
	// for every retained, removed and added fixture target, not only the R-S3 id.
	for _, row := range rows {
		raw, err := json.Marshal(row)
		require.NoError(t, err)
		for _, target := range []string{"987654321", "888888888", "777777777", "666666666"} {
			assert.NotContains(t, string(raw), target, "A2.1/R-S3: no raw target anywhere in audit event %v", row["event"])
			assert.NotContains(t, string(raw), "telegram:"+target, "A2.1/R-S3: no canonical target anywhere in audit event %v", row["event"])
		}
	}
	asked := q7AuditEvent(t, rows, audit.EventToolPolicyAskRequested, entry.ApprovalID)
	q7AuditIdentity(t, asked, entry, q7FollowupDigest(t, entry.Args))
	preview, ok := asked["args_preview"].(map[string]any)
	require.True(t, ok, "instrument: actual ask audit preview is an object")
	require.NotEmpty(t, preview, "instrument: projection checks must never pass on an empty preview")
	// A2.1 names readable identity facts without specifying their nesting.
	// Accept flat or nested identity, but require exact scalar key/value facts.
	assert.True(t, q7AuditContainsField(preview, "id", "telegram"), "A2.1: preview carries the readable connection id")
	assert.True(t, q7AuditContainsField(preview, "platform", "telegram"), "A2.1: preview carries the readable connection platform")
	for _, field := range []struct {
		key  string
		want any
	}{
		{"current_policy", "reply_only"},
		{"new_policy", "agent_may_start"},
		{"added_count", float64(1)},
		{"removed_count", float64(2)},
		{"revision", beforeRevision},
	} {
		assert.Equal(t, field.want, preview[field.key], "A2.1: exact readable non-target audit field %s", field.key)
	}
}
