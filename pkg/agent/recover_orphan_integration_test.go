// Omnipus — SIGKILL recovery integration test (V2.D)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// TestRecoverOrphan_Wired_In_SessionLoadPath is the end-to-end integration test
// for V2.D. It drives runTurn via ProcessDirect on a session whose on-disk
// transcript ends with an orphaned tool_call (no matching tool result), then
// asserts:
//
//  (a) The LLM does NOT see the orphaned tool_call_id in its history payload
//      (verified via ScenarioProvider.LastMessages() captured on the first Chat call).
//  (b) An audit event tool.policy.ask.denied with decision=deny and
//      details.reason="restart" was emitted.
//  (c) The on-disk transcript (via store.GetHistory) contains BOTH the original
//      orphaned assistant entry AND a new role=system turn_canceled_restart entry.
//
// This proves RecoverOrphanedToolCalls is wired from the session-load path in
// runTurn (FR-069 / FR-088) — the protection that had zero production callers
// before V2.D.
//
// Traces to: quizzical-marinating-frog.md — Wave V2.D

package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/dapicom-ai/omnipus/pkg/agent/testutil"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/providers"
)

// TestRecoverOrphan_Wired_In_SessionLoadPath drives the full session-load →
// RecoverOrphanedToolCalls → LLM call chain end-to-end.
//
// Setup:
//   - Construct an AgentLoop with a ScenarioProvider scripted to emit a plain
//     text reply as its first (and only) step.
//   - Seed the agent's session store with an orphaned history: one user message
//   - one assistant message with a tool_call but no matching tool result.
//   - Call ProcessDirect which routes through runTurn (same path as production).
//
// The session-load path in runTurn calls RecoverOrphanedToolCalls (V2.D wiring),
// which strips the orphaned assistant turn from the LLM context and writes the
// synthetic turn_canceled_restart system message.
func TestRecoverOrphan_Wired_In_SessionLoadPath(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	// ScenarioProvider: one step — plain text response.
	// The orphaned assistant message is stripped from context before the LLM
	// call; the LLM should just see the user message and produce this reply.
	const llmReply = "Sure, I can help with that."
	provider := testutil.NewScenario().
		WithText(llmReply)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspaceDir,
				ModelName:         "scripted-model",
				MaxTokens:         4096,
				MaxToolIterations: 5,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true,
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	// Resolve the session key that ProcessDirect will use.
	// ProcessDirect → channel="cli", chatID="direct" → DMScopeMain → "agent:main:main".
	const resolvedSessionKey = "agent:main:main"

	// Seed the default agent's session store with an orphaned history.
	// An orphaned history = assistant message with tool_calls but NO subsequent
	// tool result message (simulates a gateway SIGKILL while awaiting approval).
	const orphanToolCallID = "tc-orphan-v2d"
	const orphanToolName = "exec"

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent, "default agent must be registered")

	orphanHistory := buildOrphanedHistory(orphanToolCallID, orphanToolName)
	for _, msg := range orphanHistory {
		defaultAgent.Sessions.AddFullMessage(resolvedSessionKey, msg)
	}
	require.NoError(t, defaultAgent.Sessions.Save(resolvedSessionKey), "Save must succeed")

	// Verify the orphan is actually in the store before we call ProcessDirect.
	seedHistory := defaultAgent.Sessions.GetHistory(resolvedSessionKey)
	require.Len(t, seedHistory, 2, "seeded history must have user + orphaned-assistant messages")

	// Drive runTurn end-to-end.
	ctx := context.Background()
	finalContent, err := al.ProcessDirect(ctx, "what's the status?", resolvedSessionKey)
	require.NoError(t, err, "ProcessDirect must not fail on a session with orphaned tool calls")
	assert.Equal(t, llmReply, finalContent,
		"ProcessDirect must return the ScenarioProvider's scripted reply")

	// -------------------------------------------------------------------
	// Assert (a): the LLM prompt did NOT contain the orphaned tool_call_id.
	//
	// RecoverOrphanedToolCalls strips the orphaned assistant turn from the
	// history slice before BuildMessages is called. The ScenarioProvider
	// records the messages on each Chat call; LastMessages() returns the
	// snapshot from the first (and only) call.
	// -------------------------------------------------------------------
	lastMsgs := provider.LastMessages()
	require.NotNil(t, lastMsgs, "ScenarioProvider must have been called at least once")

	for _, msg := range lastMsgs {
		if msg.Role == "assistant" {
			for _, tc := range msg.ToolCalls {
				assert.NotEqual(t, orphanToolCallID, tc.ID,
					"LLM prompt must NOT contain the orphaned tool_call_id %q (FR-088)", orphanToolCallID)
			}
		}
	}

	// Verify no orphaned assistant message at all in the prompt.
	for _, msg := range lastMsgs {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			t.Errorf("LLM prompt must NOT contain any assistant message with tool_calls (FR-088); got: %+v", msg)
		}
	}

	// -------------------------------------------------------------------
	// Assert (b): audit event tool.policy.ask.denied with reason="restart"
	// was emitted by RecoverOrphanedToolCalls.
	// -------------------------------------------------------------------
	auditPath := filepath.Join(tmpHome, "system", "audit.jsonl")
	require.FileExists(t, auditPath,
		"audit.jsonl must exist (AuditLog=true + at least one audit event from recovery)")

	auditEntries, readErr := readAuditEntries(auditPath)
	require.NoError(t, readErr, "audit.jsonl must be parseable")

	var foundRestartDeny bool
	for _, entry := range auditEntries {
		if entry["event"] == string(audit.EventToolPolicyAskDenied) &&
			entry["decision"] == string(audit.DecisionDeny) {
			if details, ok := entry["details"].(map[string]any); ok {
				if reason, _ := details["reason"].(string); reason == "restart" {
					foundRestartDeny = true
					tc, _ := details["tool_call_id"].(string)
					assert.Equal(t, orphanToolCallID, tc,
						"audit entry must name the orphaned tool_call_id")
				}
			}
		}
	}
	assert.True(t, foundRestartDeny,
		"audit log must contain tool.policy.ask.denied with reason=restart "+
			"(emitted by RecoverOrphanedToolCalls at session-load time); entries: %v", auditEntries)

	// -------------------------------------------------------------------
	// Assert (c): on-disk transcript contains BOTH the original orphaned
	// assistant entry AND the new synthetic turn_canceled_restart system entry.
	// -------------------------------------------------------------------
	fullHistory := defaultAgent.Sessions.GetHistory(resolvedSessionKey)

	var hasOrphanedAssistant bool
	var hasSyntheticSystem bool
	for _, msg := range fullHistory {
		if msg.Role == "assistant" && len(msg.ToolCalls) > 0 {
			for _, tc := range msg.ToolCalls {
				if tc.ID == orphanToolCallID {
					hasOrphanedAssistant = true
				}
			}
		}
		if msg.Role == "system" &&
			strings.Contains(msg.Content, "turn_canceled_restart") &&
			strings.Contains(msg.Content, orphanToolCallID) {
			hasSyntheticSystem = true
		}
	}

	assert.True(t, hasOrphanedAssistant,
		"on-disk transcript must PRESERVE the original orphaned assistant entry (audit trail, FR-069); "+
			"full history: %v", fullHistory)
	assert.True(t, hasSyntheticSystem,
		"on-disk transcript must contain the synthetic turn_canceled_restart system entry "+
			"(FR-069); full history: %v", fullHistory)

	// -------------------------------------------------------------------
	// Differentiation: if RecoverOrphanedToolCalls had NOT been wired, the
	// LLM would have received the orphaned assistant message. Confirm the
	// ScenarioProvider was called exactly once (clean turn, no retries).
	// -------------------------------------------------------------------
	assert.Equal(t, 1, provider.CallCount(),
		"ScenarioProvider must have been called exactly once — the turn must complete "+
			"cleanly without retries after orphan recovery")
}

// TestRecoverOrphan_CleanSession_IsNoOp verifies that ProcessDirect on a clean
// session (no orphaned tool calls) does NOT inject any synthetic system message
// and does NOT change the session history length relative to a clean run.
//
// This is the differentiation / no-side-effects test for the wiring.
func TestRecoverOrphan_CleanSession_IsNoOp(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	const llmReply = "Everything is fine."
	provider := testutil.NewScenario().WithText(llmReply)

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace:         workspaceDir,
				ModelName:         "scripted-model",
				MaxTokens:         4096,
				MaxToolIterations: 5,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	defer al.Close()

	const resolvedSessionKey = "agent:main:main"

	defaultAgent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, defaultAgent)

	// Seed with a CLEAN history (tool call + matching result).
	cleanHistory := buildCleanHistory("tc-clean", "exec")
	for _, msg := range cleanHistory {
		defaultAgent.Sessions.AddFullMessage(resolvedSessionKey, msg)
	}
	require.NoError(t, defaultAgent.Sessions.Save(resolvedSessionKey))

	priorLen := len(defaultAgent.Sessions.GetHistory(resolvedSessionKey))

	_, err := al.ProcessDirect(context.Background(), "ping", resolvedSessionKey)
	require.NoError(t, err, "ProcessDirect must succeed on a clean session")

	afterHistory := defaultAgent.Sessions.GetHistory(resolvedSessionKey)

	// The history grew (new user + assistant messages were added by the turn),
	// but NO synthetic turn_canceled_restart message should have been injected.
	hasRestartMsg := false
	for _, msg := range afterHistory {
		if msg.Role == "system" && strings.Contains(msg.Content, "turn_canceled_restart") {
			hasRestartMsg = true
		}
	}
	assert.False(t, hasRestartMsg,
		"clean session must NOT have a turn_canceled_restart system entry injected "+
			"(RecoverOrphanedToolCalls must be a no-op on clean sessions)")
	assert.Greater(t, len(afterHistory), priorLen,
		"history should have grown by at least the new user and assistant messages")

	// Verify the ScenarioProvider received the clean tool result in its prompt
	// (no orphan stripping occurred).
	lastMsgs := provider.LastMessages()
	require.NotNil(t, lastMsgs)
	hasTool := false
	for _, msg := range lastMsgs {
		if msg.Role == "tool" {
			hasTool = true
		}
	}
	assert.True(t, hasTool,
		"LLM prompt must contain the clean tool result message (no orphan stripping); "+
			"last messages: %v", lastMsgs)
}

// buildOrphanedHistory and buildCleanHistory are defined in session_recovery_test.go
// (same package) — they are reused here without redeclaration.

// Ensure providers is used (the import is needed for providers.Message in
// the helper functions called from this file via session_recovery_test.go).
var _ providers.Message
