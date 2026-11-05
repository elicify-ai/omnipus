package gateway

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestChannelsQ2B12_ExecutedArgumentsExactlyMatchModelArguments(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "jim")
	sid := cq2Caller(t, f, "mia", false, false)
	observer := &cq2ExecuteObserver{}
	require.NoError(t, f.al.MountHook(agent.NamedHook("cq2-execution-arguments", observer)))
	t.Cleanup(func() { f.al.UnmountHook("cq2-execution-arguments") })
	args := cq2Args("telegram", "agent_may_start", []string{" 222 ", "telegram:222"})
	original := cq2JSON(t, args)
	const callID = "cq2-s1-args"
	events := cq2Start(t, f, sid, "mia", callID, args)
	entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
	cq2Action(t, f, entry, "allow_once")
	q7WaitTurn(t, f, events, sid)
	observer.mu.Lock()
	calls := append([]*agent.ToolResultHookResponse{}, observer.calls...)
	observer.mu.Unlock()
	require.Len(t, calls, 1, "instrument: observer saw actual dispatch, not merely a transcript copy")
	require.Equal(t, original, cq2JSON(t, calls[0].Arguments), "Q2-B/S1: no expected_revision or card fact was injected into Execute's arguments")
	require.Equal(t, original, cq2JSON(t, args), "Q2-B/S1: provider response map also remains unchanged")
	require.NotNil(t, calls[0].Result)
	q7Saved(t, calls[0].Result, "agent_may_start", []string{"telegram:222"})
	require.Equal(t, original, cq2JSON(t, cq2TranscriptCall(t, f, sid, callID).Parameters))
	t13AssertOutbound(t, f, "telegram", "agent_may_start", []string{"telegram:222"})
}

func cq2PrivateStateAbsent(t *testing.T, text, revision string, storedTargets []string) {
	t.Helper()
	require.NotEmpty(t, text, "instrument: inspect a real refusal/result, not an empty string")
	for _, target := range storedTargets {
		assert.NotContains(t, text, target, "Q2-B §5: an unrelated stored target must not reach the agent")
	}
	assert.NotContains(t, text, revision, "Q2-B §5: never reveal the server-read conflict token")
	assert.NotContains(t, text, `"reply_only"`, "Q2-B §5: never serialize the stored policy in the agent result")
	for _, key := range []string{"current_outbound", "new_outbound", "connection", "added", "removed"} {
		assert.NotContains(t, text, `"`+key+`"`, "Q2-B §5: card-only field %s never reaches the agent", key)
	}
}

func TestChannelsQ2B13_RefusalResultsNeverDiscloseStoredState(t *testing.T) {
	cases := append(cq2WriterCases(),
		cq2RefusalCase{"stale_revision", "telegram", "CONFLICT", "stale_revision", []string{"222"}},
		cq2RefusalCase{"own_connection", "telegram", "OWN_CONNECTION", "", []string{"222"}},
		cq2RefusalCase{"not_configured", "telegram", "CHANNEL_NOT_FOUND", "", []string{}},
		cq2RefusalCase{"invalid_input", "telegram", "INVALID_INPUT", "", []string{}},
	)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, sid, args := cq2RefusalSetup(t, tc)
			// None of these canaries is in the model's request. The current
			// policy is reply_only, while valid requests ask for agent_may_start.
			canaries := []string{tc.platform + ":9876543199", tc.platform + ":9876543188"}
			cq2Stored(t, f, tc.platform, "reply_only", canaries)
			revision := t14Read(t, f, tc.platform).Revision
			args["expected_revision"] = revision
			if tc.name == "stale_revision" {
				args["expected_revision"] = "made-up-revision"
			}
			if tc.name == "own_connection" {
				require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
					inst := cfg.Channels[tc.platform]
					inst.Identity = &config.ChannelIdentity{Kind: "agent", ID: "mia"}
					cfg.Channels[tc.platform] = inst
					return config.SaveConfig(f.f1277.configPath, cfg)
				}))
				revision = t14Read(t, f, tc.platform).Revision
				args["expected_revision"] = revision
			}
			if tc.name == "not_configured" {
				args["id"] = "unconfigured-q2b"
			}
			if tc.name == "invalid_input" {
				args["policy"] = "not-a-policy"
			}
			if tc.name == "saved_live_mismatch" {
				// Recreate saved-only divergence after planting the canaries.
				t32FailedCommonSave(t, f, false, t32RebindDoc(t))
			}
			// Resolve the real card on old code so this privacy control can
			// inspect the ACTUAL writer refusal independently of cases 08/10.
			callID := "cq2-private-" + tc.name
			events := cq2Start(t, f, sid, "mia", callID, args)
			entry := q7FollowupPendingOrCompleted(t, f, events, sid, callID)
			if entry != nil {
				cq2Action(t, f, entry, "allow_once")
				q7WaitTurn(t, f, events, sid)
			}
			cq2Failure(t, f.provider.lastToolResult(), tc.code, tc.reason)
			cq2PrivateStateAbsent(t, f.provider.lastToolResult(), revision, canaries)
		})
	}
	t.Run("omnipus_only", func(t *testing.T) {
		// Missing identity cannot be produced by an identified real-loop turn;
		// use the same production-wired tool's direct admission boundary.
		f := q7Fixture(t, "telegram", "telegram", "jim")
		canaries := []string{"telegram:9876543199", "telegram:9876543188"}
		cq2Stored(t, f, "telegram", "reply_only", canaries)
		revision := t14Read(t, f, "telegram").Revision
		before := t13Before(t, f)
		result := q7ExecuteAs(t, f, "", q7Args("telegram", "agent_may_start", revision, []string{"222"}))
		q7Error(t, result, "OMNIPUS_ONLY")
		cq2PrivateStateAbsent(t, result.ForLLM, revision, canaries)
		t13Unchanged(t, f, before)
	})
}

func TestChannelsQ2B14_CardFactsStayOutOfTranscriptAndModelContext(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "jim")
	sid := cq2Caller(t, f, "mia", false, false)
	cq2Stored(t, f, "telegram", "reply_only", []string{"telegram:9876543199"})
	oldRevision := t14Read(t, f, "telegram").Revision
	args := cq2Args("telegram", "agent_may_start", []string{"222"})
	const callID = "cq2-card-privacy"
	events := cq2Start(t, f, sid, "mia", callID, args)
	entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
	require.Contains(t, entry.Args, "current_outbound", "instrument: privacy check must start from a card that actually has private facts")
	require.Equal(t, cq2JSON(t, map[string]any{"policy": "reply_only", "targets": []string{"telegram:9876543199"}}), cq2JSON(t, entry.Args["current_outbound"]))
	require.Equal(t, oldRevision, entry.Args["revision"])
	cq2Action(t, f, entry, "allow_once")
	q7WaitTurn(t, f, events, sid)
	call := cq2TranscriptCall(t, f, sid, callID)
	require.Equal(t, cq2JSON(t, args), cq2JSON(t, call.Parameters))
	require.Equal(t, "success", call.Status)
	raw, err := json.Marshal(call)
	require.NoError(t, err)
	cq2PrivateStateAbsent(t, string(raw), oldRevision, []string{"telegram:9876543199"})
	cq2PrivateStateAbsent(t, f.provider.lastToolResult(), oldRevision, []string{"telegram:9876543199"})
	q7Saved(t, &tools.ToolResult{ForLLM: f.provider.lastToolResult()}, "agent_may_start", []string{"telegram:222"})
	f.provider.mu.Lock()
	prompts := f.provider.prompts
	promptJSON, marshalErr := json.Marshal(prompts)
	f.provider.mu.Unlock()
	require.NoError(t, marshalErr)
	// ToolSearch descriptions legitimately name fields and policies. Inspect
	// only the concrete model call and its tool result, not the entire catalog.
	seenCall, seenResult := false, false
	for _, turns := range prompts {
		for _, messages := range turns {
			for _, msg := range messages {
				for _, tc := range msg.ToolCalls {
					if tc.ID == callID {
						seenCall = true
						assert.Equal(t, cq2JSON(t, args), cq2JSON(t, tc.Arguments))
					}
				}
				if msg.Role == "tool" && msg.ToolCallID == callID {
					seenResult = true
					cq2PrivateStateAbsent(t, msg.Content, oldRevision, []string{"telegram:9876543199"})
				}
			}
		}
	}
	require.True(t, seenCall, "instrument: provider really saw the concrete call")
	require.True(t, seenResult, "instrument: provider really saw this call's result")
	require.NotContains(t, string(promptJSON), "telegram:9876543199", "Q2-B: private stored target never entered any provider message")
}

func TestChannelsQ2B15_GodModeAndAutoStillPrepareAndAsk(t *testing.T) {
	f := q7Fixture(t, "discord", "discord", "jim")
	sid := cq2Caller(t, f, "mia", true, true)
	require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
		inst := cfg.Channels["discord"]
		inst.Enabled = true
		cfg.Channels["discord"] = inst
		return config.SaveConfig(f.f1277.configPath, cfg)
	}))
	adapter := cq2DiscordEdge(f, true, "")
	args := q7Args("discord", "agent_may_start", t14Read(t, f, "discord").Revision, []string{"222"})
	const callID = "cq2-god-auto"
	events := cq2Start(t, f, sid, "mia", callID, args)
	entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
	assert.Equal(t, []string{"discord:222"}, adapter.lookupSnapshot(), "Q2-B/T-10: real preparation occurs BEFORE the card in God Mode with Auto")
	assert.Contains(t, entry.Args, "current_outbound")
	require.Equal(t, 1, cq2RequestCount(f, sid, callID))
	cq2Action(t, f, entry, "allow_once")
	q7WaitTurn(t, f, events, sid)
	t13AssertOutbound(t, f, "discord", "agent_may_start", []string{"discord:222"})
	require.Equal(t, []string{"discord:222"}, adapter.lookupSnapshot(), "Q1 B: exactly the pre-card verification, not a second lookup")
}

func TestChannelsQ2B16_HeadlessOrGlobalDenyNeverPreview(t *testing.T) {
	for _, kind := range []string{"headless", "global_deny"} {
		t.Run(kind, func(t *testing.T) {
			f := q7Fixture(t, "discord", "discord", "jim")
			sid := cq2Caller(t, f, "mia", false, false)
			require.NoError(t, f.al.MutateConfig(func(cfg *config.Config) error {
				inst := cfg.Channels["discord"]
				inst.Enabled = true
				cfg.Channels["discord"] = inst
				if kind == "global_deny" {
					cfg.Sandbox.ToolPolicies["set_channel_outbound"] = "deny"
				}
				return config.SaveConfig(f.f1277.configPath, cfg)
			}))
			_, err := f.al.UpsertAgentFast(f.al.GetConfig(), "mia")
			require.NoError(t, err)
			q7WireRealTools(t, f)
			require.NoError(t, wireWU6ScriptedProviderPool(f.al, f.provider))
			adapter := cq2DiscordEdge(f, true, "")
			// A matching valid revision and live resolver would demand preview
			// lookup if the verdict/headless guards ran in the wrong order.
			args := q7Args("discord", "agent_may_start", t14Read(t, f, "discord").Revision, []string{"222"})
			const callID = "cq2-verdict-first"
			before := t13Before(t, f)
			if kind == "headless" {
				f.provider.setDefaultScript(q7Script(callID, args))
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, err := f.al.ProcessScheduled(ctx, "mia", sid, "change posting policy", "webchat", "cq2-headless")
				require.NoError(t, err)
			} else {
				events := cq2Start(t, f, sid, "mia", callID, args)
				cq2NoCard(t, f, events, sid, callID)
			}
			require.Empty(t, adapter.lookupSnapshot(), "Q2-B: denied/headless calls never invoke real Discord preview")
			require.Zero(t, cq2RequestCount(f, sid, callID))
			require.Contains(t, f.provider.lastToolResult(), "denied", "instrument: the scripted call was attempted and refused")
			rows := q7AuditRead(t, f, sid, "set_channel_outbound", false)
			denials := 0
			for _, row := range rows {
				// Generic policy denials use audit.Entry's top-level tool/agent,
				// unlike channel.outbound.write's structured fields object.
				if row["event"] == audit.EventToolPolicyDenyAttempted && row["tool"] == "set_channel_outbound" && row["agent_id"] == "mia" {
					denials++
					require.NotEmpty(t, row["session_id"], "Q2-B: actual denial audit has a session identity")
				}
			}
			require.Equal(t, 1, denials, "Q2-B: this isolated denied no-card call has exactly one real policy-denial audit")
			t13Unchanged(t, f, before)
		})
	}
}

func TestChannelsQ2B18_DenyCardWritesNoOutboundOutcome(t *testing.T) {
	f := q7Fixture(t, "telegram", "telegram", "jim")
	sid := cq2Caller(t, f, "mia", false, false)
	args := q7Args("telegram", "agent_may_start", t14Read(t, f, "telegram").Revision, []string{"222"})
	before := t13Before(t, f)
	const callID = "cq2-human-deny"
	events := cq2Start(t, f, sid, "mia", callID, args)
	entry := q7PendingOrFailOnTurnEnd(t, f, events, sid, callID)
	require.Equal(t, 1, cq2RequestCount(f, sid, callID))
	require.Empty(t, cq2OutcomeRows(t, f, sid, callID), "Q2-B: preview is not a write event")
	cq2Action(t, f, entry, "deny")
	q7WaitTurn(t, f, events, sid)
	t13Unchanged(t, f, before)
	require.Empty(t, cq2OutcomeRows(t, f, sid, callID), "Q2-B: denying the card must not create a writer outcome")
	rows := q7AuditRead(t, f, sid, "set_channel_outbound", false)
	q7AuditEvent(t, rows, audit.EventToolPolicyAskRequested, entry.ApprovalID)
	terminal := q7AuditEvent(t, rows, audit.EventToolPolicyAskDenied, entry.ApprovalID)
	require.Equal(t, "user", terminal["reason"])
	require.Contains(t, f.provider.lastToolResult(), "denied")
}
