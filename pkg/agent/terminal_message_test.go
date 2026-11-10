// Copyright (c) 2026 Omnipus contributors
// License: MIT

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// RED plan for #1081 stream C / duplicate-tool-definition early return:
//   - Founder requirement: every real stop delivers a terminal message, live
//     AND durably. A system-only context message or turn.end is not that notice.
//   - Rejection oracle: tool-registry-redesign-spec.md FR-066 requires failure
//     of the provider call on an internal duplicate-name invariant violation.
//   - User-copy oracle: contracts/components/schemas/LLMError.yaml,
//     x-user-messages.unknown. No new error code or copy is invented here —
//     the expected text is read from the canonical catalogue
//     (UserMessageForCode(CodeUnknown)), not pasted by hand; see
//     pkg/api/generated/llm_error_no_hardcopy_test.go.
//   - Real boundary: runAgentLoop, request assembly, registry, event bus, and
//     session storage are real. The provider and external tool descriptors are
//     fixtures. Mutating a descriptor constructs FR-066's controlled duplicate
//     registry without mocking the invariant or adding a production test hook.
//   - Cases: duplicate before the first request; duplicate on the next request
//     after narration and a tool result; unique names with a normal final answer.
//   - CHECK probes (deferred): omit only persist, omit only emit, persist only
//     the synthetic system payload. GREEN and mutation proof belong to CHECK.
//   - Not covered: production collision races, frontend rendering, cancellation,
//     limit validation, or historical occurrence of this exit.
var terminalDuplicateUnknownNotice = UserMessageForCode(CodeUnknown)

func TestRunTurn_DuplicateToolDefinitions_DeliversTerminalMessage(t *testing.T) {
	const probeName = "terminal_duplicate_probe"
	const spareName = "terminal_duplicate_spare"
	const narration = "I will run the probe."
	const finalAnswer = "The probe completed."
	cases := []struct {
		name        string
		beforeFirst bool
		afterTool   bool
		wantCalls   int
	}{
		{"before_first_provider_request", true, false, 0},
		{"after_narrated_tool_round", false, true, 1},
		{"unique_definitions_control", false, false, 2},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("OMNIPUS_HOME", t.TempDir())
			probe := &dupTool{name: probeName, scopeVal: tools.ScopeCore, sourceTag: "builtin"}
			spare := &dupTool{name: spareName, scopeVal: tools.ScopeCore, sourceTag: "builtin"}
			provider := &truncationScriptedProvider{steps: []truncationScriptStep{
				{
					content: narration, finishReason: "tool_calls",
					toolCalls: []providers.ToolCall{{ID: "terminal-duplicate-probe-call", Function: &providers.FunctionCall{
						Name: probeName, Arguments: "{}",
					}}},
				},
				{content: finalAnswer, finishReason: "stop"},
			}}
			// Two rounds admit the next request after the tool. This is not an
			// iteration-cap fixture: the normal control completes on round two.
			al, inst, store, sessionID := newTruncationTestHarness(t, provider, 2, 0)
			al.RegisterTool(probe)
			al.RegisterTool(spare)
			inst.StoreToolPolicy(&tools.ToolPolicyCfg{Policies: map[string]config.ToolPolicy{
				probeName: config.ToolPolicyAllow,
				spareName: config.ToolPolicyAllow,
			}})
			if tc.beforeFirst {
				spare.name = probeName
			}
			if tc.afterTool {
				// The first assembly has unique names. The external descriptor
				// changes while its provider response is in flight, so the real
				// next assembly (not a direct invariant call) detects the fault.
				provider.steps[0].onCall = func() { spare.name = probeName }
			}
			sub := al.SubscribeEvents(128)
			defer al.UnsubscribeEvents(sub.ID)
			answer, err := al.runAgentLoop(context.Background(), inst, processOptions{
				SessionKey: sessionID, Channel: "webchat", ChatID: sessionID,
				UserMessage: "Run the probe.", DefaultResponse: defaultResponse,
				TranscriptSessionID: sessionID, TranscriptStore: store,
			})
			require.Equal(t, tc.wantCalls, provider.CallCount(),
				"fixture: the rejected request must never reach the provider")
			events := collectEventStream(sub.C) // Emit is synchronous; the turn has returned.
			end, ok := findEvent(events, EventKindTurnEnd)
			require.True(t, ok, "fixture: the real turn must reach its terminal lifecycle event")
			endPayload, ok := end.Payload.(TurnEndPayload)
			require.True(t, ok, "fixture: expected TurnEndPayload, got %T", end.Payload)
			entries, readErr := store.ReadTranscript(sessionID)
			require.NoError(t, readErr)
			var terminalErrors []session.TranscriptEntry
			var assistantText []string
			var toolCalls int
			for _, entry := range entries {
				if entry.Status == "error" {
					terminalErrors = append(terminalErrors, entry)
				}
				if entry.Role == "assistant" && entry.Content != "" {
					assistantText = append(assistantText, entry.Content)
				}
				toolCalls += len(entry.ToolCalls)
			}
			var liveErrors []ErrorPayload
			for _, event := range events {
				if event.Kind == EventKindError {
					payload, valid := event.Payload.(ErrorPayload)
					require.True(t, valid, "fixture: expected typed ErrorPayload, got %T", event.Payload)
					liveErrors = append(liveErrors, payload)
				}
			}

			if !tc.beforeFirst && !tc.afterTool {
				require.NoError(t, err)
				assert.Equal(t, finalAnswer, answer, "control: normal completion is still reachable")
				assert.Equal(t, TurnEndStatusCompleted, endPayload.Status)
				assert.Equal(t, []string{narration, finalAnswer}, assistantText)
				assert.Empty(t, terminalErrors, "control: success must not invent a durable error")
				assert.Empty(t, liveErrors, "control: success must not invent a live error")
				return
			}
			require.ErrorContains(t, err, "dedup invariant violated", "FR-066: duplicate names fail assembly")
			assert.Contains(t, err.Error(), probeName, "FR-066: diagnostic identifies the duplicate name")
			assert.Equal(t, TurnEndStatusError, endPayload.Status)
			if tc.afterTool {
				require.Positive(t, toolCalls, "fixture: a real tool-call record precedes the stop")
				assert.Equal(t, []string{narration}, assistantText,
					"the earlier narration remains; it is not a substitute for the terminal error")
			} else {
				assert.Zero(t, toolCalls, "fixture: initial rejection executes no tool")
				assert.Empty(t, assistantText, "fixture: initial rejection has no prior narration")
			}
			// Non-fatal assertions deliberately observe BOTH independent gaps
			// even when the first terminal surface is missing.
			if assert.Len(t, terminalErrors, 1, "TRANSCRIPT must record exactly one classified terminal error, not just synthetic context") {
				terminal := terminalErrors[0]
				assert.Equal(t, "unknown", terminal.ErrorCode)
				assert.Equal(t, terminalDuplicateUnknownNotice, terminal.Content)
				assert.Equal(t, inst.ID, terminal.AgentID)
				assert.Equal(t, terminal.ID, entries[len(entries)-1].ID,
					"the stop explanation must be the turn's final transcript entry")
			}
			if assert.Len(t, liveErrors, 1, "LIVE must emit exactly one typed terminal error; turn.end alone is not an explanation") {
				assert.Equal(t, "unknown", liveErrors[0].Code)
				assert.Equal(t, terminalDuplicateUnknownNotice, liveErrors[0].Message)
				assert.Equal(t, sessionID, liveErrors[0].SessionID)
				assert.Equal(t, sessionID, liveErrors[0].ChatID)
			}
		})
	}
}
