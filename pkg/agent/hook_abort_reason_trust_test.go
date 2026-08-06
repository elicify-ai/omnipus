// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// hook_abort_reason_trust_test.go — FIX 6 regression: trustedInternalStageSet
// (turn.go) omitted "hooks" — the literal stage hookAbortError ALWAYS passes
// to appendErrorTranscript, regardless of which HookInterceptor stage
// (before_llm/after_llm/before_tool/after_tool) actually triggered the abort.
// Untrusted + a hook-authored decision.Reason that happens to contain a
// pinned classifier substring (e.g. "safety", part of contentPolicySubstrings)
// meant the curated reason was silently replaced with generic classifier
// boilerplate before it reached the transcript.

package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// beforeToolAbortHook is a ToolInterceptor that unconditionally hard-aborts
// the turn from BeforeTool with a curated Reason that would be reclassified
// by the shared classifier if it were ever re-run against it (the reason
// text below contains "safety", a pinned contentPolicySubstrings hit).
type beforeToolAbortHook struct {
	reason string
}

func (h *beforeToolAbortHook) BeforeTool(
	_ context.Context,
	call *ToolCallHookRequest,
) (*ToolCallHookRequest, HookDecision, error) {
	return call, HookDecision{Action: HookActionAbortTurn, Reason: h.reason}, nil
}

func (h *beforeToolAbortHook) AfterTool(
	_ context.Context,
	result *ToolResultHookResponse,
) (*ToolResultHookResponse, HookDecision, error) {
	return result, HookDecision{Action: HookActionContinue}, nil
}

// TestHookAbort_BeforeTool_CuratedReasonSurvivesClassification is the FIX 6
// regression. A BeforeTool hook aborts the turn with a curated reason that
// contains a pinned classifier substring. Before the fix, "hooks" was not in
// trustedInternalStageSet, so appendErrorTranscript's write choke point
// re-classified the message via TranslateLLMError and — since the reason's
// "safety" substring matches contentPolicySubstrings — overwrote it with the
// generic content-policy copy instead of preserving it verbatim.
func TestHookAbort_BeforeTool_CuratedReasonSurvivesClassification(t *testing.T) {
	const curated = "blocked by policy: this request touches safety-sensitive data"

	provider := testutil.NewScenario().WithToolCall("echo_text", `{"text":"x"}`)
	al, agent, cleanup := newHookTestLoop(t, provider)
	defer cleanup()

	al.RegisterTool(&echoTextTool{})
	// No-default-policy model (CLAUDE.md hard constraint 6).
	agent.StoreToolPolicy(&tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{"echo_text": "allow"},
	})
	require.NoError(t, al.MountHook(NamedHook("before-tool-abort", &beforeToolAbortHook{reason: curated})),
		"mounting the abort hook must succeed")

	store := al.GetSessionStore()
	require.NotNil(t, store, "shared session store must be non-nil")
	meta, err := store.NewSession(session.SessionTypeChat, "cli", agent.ID)
	require.NoError(t, err)
	sessionID := meta.ID

	_, err = al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey:          "hook-abort-session",
		Channel:             "cli",
		ChatID:              sessionID,
		UserMessage:         "run the tool",
		DefaultResponse:     defaultResponse,
		EnableSummary:       false,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.Error(t, err, "a before_tool hard abort must surface as a turn error")

	entries, rErr := store.ReadTranscript(sessionID)
	require.NoError(t, rErr)
	var errEntry *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem && entries[i].Status == "error" {
			cp := entries[i]
			errEntry = &cp
			break
		}
	}
	require.NotNil(t, errEntry, "transcript must carry the hook-abort system entry; entries: %+v", entries)
	assert.Contains(t, errEntry.Content, curated,
		"the persisted transcript must preserve the hook's curated reason verbatim, "+
			"not the generic classifier copy; got %q", errEntry.Content)
}

// TestIsTrustedInternalStage_HookStages locks the trustedInternalStageSet
// membership directly: every stage hookAbortError can reach (the literal
// "hooks" stage it always passes to appendErrorTranscript, plus the
// defensive before_tool/after_tool entries) must be trusted, alongside the
// pre-existing before_llm/after_llm/model_switch/etc. entries. An unrelated
// stage must remain untrusted.
func TestIsTrustedInternalStage_HookStages(t *testing.T) {
	cases := []struct {
		name        string
		stage, kind string
		want        bool
	}{
		{"hooks/error trusted — hookAbortError's actual appendErrorTranscript stage", "hooks", "error", true},
		{"before_tool/error trusted (defensive)", "before_tool", "error", true},
		{"after_tool/error trusted (defensive)", "after_tool", "error", true},
		{"before_llm/error trusted (pre-existing)", "before_llm", "error", true},
		{"after_llm/error trusted (pre-existing)", "after_llm", "error", true},
		{"unrelated stage/kind is not trusted", "runTurn", "error", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, isTrustedInternalStage(tc.stage, tc.kind))
		})
	}
}
