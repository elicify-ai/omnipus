package gateway

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A2.2 and section 4: S1 still pins every model argument exactly; only the six
// named server facts may accompany them. Arbitrary extra keys are not accepted.
func cq2RequireCardModelArguments(t *testing.T, model, card map[string]any) {
	t.Helper()
	require.NotEmpty(t, model, "instrument: compare an actual model request")
	for key, want := range model {
		require.Contains(t, card, key, "A2.2: card preserves every model argument key")
		require.Equal(t, want, card[key], "A2.2/S1: model argument %s is unchanged", key)
	}
	extra := []string{}
	for key := range card {
		if _, modelKey := model[key]; !modelKey {
			extra = append(extra, key)
		}
	}
	require.ElementsMatch(t, []string{"connection", "current_outbound", "new_outbound", "added", "removed", "revision"}, extra,
		"A2.2: all section-4 facts and no other keys accompany the model arguments")
}

// A1/A2 audit oracle: exact result strings, count and correlation, inspecting
// the full outcome record for target leaks. No production audit emitter is used.
func cq2RequireVerificationAudit(t *testing.T, f *wu6Fixture, sid, callID, approvalID, channelID string, wantResults, forbiddenTargets []string) {
	t.Helper()
	rows := cq2OutcomeRows(t, f, sid, callID)
	require.Len(t, rows, 1, "A1: one actual writer outcome for this call")
	require.Equal(t, "saved", rows[0]["outcome"])
	require.Equal(t, approvalID, rows[0]["approval_id"])
	require.Equal(t, "mia", rows[0]["agent_id"])
	require.Equal(t, channelID, rows[0]["channel_id"])
	require.NotEmpty(t, rows[0]["turn_id"], "instrument: saved outcome is correlated to its turn")
	require.Contains(t, rows[0], "target_verifications", "A1: saved records always contain the list, including when empty")
	assert.Equal(t, cq2JSON(t, wantResults), rows[0]["target_verifications"], "A1/A2.4: exact ordered verification results, not target values")
	assert.Equal(t, float64(len(wantResults)), rows[0]["target_count"], "A1: one result per saved target")
	assert.Equal(t, t14Read(t, f, channelID).Revision, rows[0]["revision"])
	var records []map[string]any
	for _, record := range q7AuditRead(t, f, sid, "set_channel_outbound", false) {
		fields, _ := record["fields"].(map[string]any)
		if record["event"] == audit.EventChannelOutboundWrite && fields["tool_call_id"] == callID && fields["session_id"] == sid {
			records = append(records, record)
		}
	}
	require.Len(t, records, 1, "instrument: privacy assertion observes one complete correlated outcome record")
	raw, err := json.Marshal(records[0])
	require.NoError(t, err)
	for _, target := range forbiddenTargets {
		assert.NotContains(t, string(raw), target, "A1: no canonical target anywhere in the writer outcome")
		_, rawID, hasPrefix := strings.Cut(target, ":")
		require.True(t, hasPrefix, "instrument: forbidden fixture target is canonical")
		assert.NotContains(t, string(raw), `"`+rawID+`"`, "A1: no raw target value anywhere in the writer outcome")
	}
}

func TestChannelsQ2B_A2_DirectWriterAuditsVerificationAtSave(t *testing.T) {
	f := q7Fixture(t, "discord", "discord", "jim")
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		inst := cfg.Channels["discord"]
		inst.Enabled = true
		cfg.Channels["discord"] = inst
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	adapter := cq2DiscordEdge(f, true, "")
	const sid, callID = "cq2-a2-direct-session", "cq2-a2-direct-call"
	ctx := tools.WithAgentID(context.Background(), "mia")
	ctx = tools.WithTranscriptSessionID(ctx, sid)
	ctx = tools.WithToolCallID(ctx, callID)
	ctx = tools.WithTurnID(ctx, "cq2-a2-direct-turn")
	tool, present := f.api.builtinRegistry.Get("set_channel_outbound")
	require.True(t, present, "instrument: execute the production-wired tool, not an invented audit callback")
	args := q7Args("discord", "agent_may_start", t14Read(t, f, "discord").Revision, []string{"700"})
	result := tool.Execute(ctx, args) // Explicit revision; no preparation, card or approval pin.
	require.NotNil(t, result)
	q7Saved(t, result, "agent_may_start", []string{"discord:700"})
	t13AssertOutbound(t, f, "discord", "agent_may_start", []string{"discord:700"})
	require.Equal(t, []string{"discord:700"}, adapter.lookupSnapshot(), "A2.4: the real writer verified the new target at save time")
	require.Zero(t, cq2RequestCount(f, sid, callID), "A2.4: direct writer call had no card")
	cq2RequireVerificationAudit(t, f, sid, callID, "", "discord", []string{"verified_at_save"}, []string{"discord:700"})
}

func TestChannelsQ2B_A2_UnreadableSavedConfigRefusesBeforeCard(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "jim")
	sid := cq2Caller(t, f, "mia", false, false)
	before := t13Before(t, f)
	backup := f.f1277.configPath + ".a2-unavailable"
	require.NoError(t, os.Rename(f.f1277.configPath, backup), "instrument: saved settings are genuinely unreadable at their configured path")
	t.Cleanup(func() { require.NoError(t, os.Rename(backup, f.f1277.configPath)) })
	_, readErr := os.ReadFile(f.f1277.configPath)
	require.ErrorIs(t, readErr, os.ErrNotExist, "instrument: a real config read fails before the call")
	const callID = "cq2-a2-preview-failed"
	args := cq2Args("telegram", "agent_may_start", []string{"700"})
	events := cq2Start(t, f, sid, "mia", callID, args)
	cq2NoCard(t, f, events, sid, callID)
	failure := cq2Failure(t, f.provider.lastToolResult(), "CONFIG_SAVE_FAILED", "")
	assert.Equal(t, "could not save; nothing was changed", failure["message"], "A2.5: fixed safe agent-facing preview failure text")
	cq2RefusedAudit(t, f, sid, "mia", callID, "telegram", "preview_failed")
	rows := cq2OutcomeRows(t, f, sid, callID)
	require.NotContains(t, rows[0], "target_verifications", "A1: preview refusal has no verification-result list")
	_, afterErr := os.ReadFile(f.f1277.configPath)
	require.ErrorIs(t, afterErr, os.ErrNotExist, "A2.5: no writer recreated the unavailable config file")
	backupBytes, err := os.ReadFile(backup)
	require.NoError(t, err)
	require.Equal(t, before.disk, backupBytes, "A2.5: the saved config bytes remain unchanged")
	require.Equal(t, before.live.Channels, f.al.GetConfig().Channels, "A2.5: no live settings changed")
}

func TestChannelsQ2B_A1_SavedAuditCoversNotRequiredAndEmptyTargets(t *testing.T) {
	for _, tc := range []struct {
		name                             string
		stored, raw, canonical, verified []string
	}{
		{"not_required", []string{}, []string{"700"}, []string{"telegram:700"}, []string{"not_required"}},
		{"empty_saved_list", []string{"telegram:700"}, []string{}, []string{}, []string{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := q7Fixture(t, "telegram", "telegram", "jim")
			sid := cq2Caller(t, f, "mia", false, false)
			cq2Stored(t, f, "telegram", "reply_only", tc.stored)
			callID := "cq2-a1-boundary-" + tc.name
			args := cq2Args("telegram", "agent_may_start", tc.raw)
			events := cq2Start(t, f, sid, "mia", callID, args)
			entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
			cq2RequireCardModelArguments(t, args, entry.Args)
			cq2Action(t, f, entry, "allow_once")
			q7WaitTurn(t, f, events, sid)
			q7Saved(t, &tools.ToolResult{ForLLM: f.provider.lastToolResult()}, "agent_may_start", tc.canonical)
			t13AssertOutbound(t, f, "telegram", "agent_may_start", tc.canonical)
			forbidden := append(append([]string{}, tc.stored...), tc.canonical...)
			cq2RequireVerificationAudit(t, f, sid, callID, entry.ApprovalID, "telegram", tc.verified, forbidden)
		})
	}
}
