// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

//go:build goolm && stdjson

package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// newSwitchTestAgentLoop builds an AgentLoop with the models needed by the
// switch-time re-window tests registered in cfg.Providers so that
// ApplyAgentModel (which handleModelSwitch now calls to orchestrate the
// provider+candidates swap alongside Model) can resolve them.
//
// The ModelName is the public identifier, and the Model field is the
// provider-prefixed form ("openai/<name>") so the known-protocol factory can
// build a stub provider. Protocol = "openai" is a recognized protocol prefix.
func newSwitchTestAgentLoop(t *testing.T, models ...string) (al *AgentLoop, cfg *config.Config, cleanup func()) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("SWITCH_TEST_KEY", "switch-test-key")
	mkProvider := func(name string) *config.ModelConfig {
		return &config.ModelConfig{
			ModelName: name,
			Model:     "openai/" + name,
			Provider:  "openai",
			APIBase:   "http://127.0.0.1:1",
			APIKeyRef: "SWITCH_TEST_KEY",
		}
	}
	cfgProviders := make([]*config.ModelConfig, 0, len(models))
	for _, m := range models {
		cfgProviders = append(cfgProviders, mkProvider(m))
	}
	cfg = &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				ModelName:         "test-model",
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
		},
		Providers: cfgProviders,
	}
	if len(cfgProviders) == 0 {
		cfg.Providers = append(cfg.Providers, mkProvider("test-model"))
	}
	mp := &mockProvider{}
	al = mustNewAgentLoop(t, cfg, bus.NewMessageBus(), mp)
	return al, cfg, func() {}
}

// --- decideSwitchCompressAction purity tests (unchanged logic) ---

// TestSwitchTimeCompress_LargerToSmaller_TriggersCompress verifies that when
// the user switches from a large-context model to a smaller-context model and
// the conversation does not fit, the switch-time re-window is triggered.
//
// BDD-11: Old=200k, new=8k, conv=50k -> re-window before switch.
func TestSwitchTimeCompress_LargerToSmaller_TriggersCompress(t *testing.T) {
	action := decideSwitchCompressAction(50000, 8000)
	assert.Equal(t, SwitchActionCompress, action,
		"50k conversation switched into an 8k window must trigger re-window")
}

// TestSwitchTimeCompress_SameWindow_NoOp verifies that switching between two
// models with the same context window does not trigger re-window.
//
// BDD-14: Old=200k, new=200k, conv=50k -> no re-window (new model fits).
func TestSwitchTimeCompress_SameWindow_NoOp(t *testing.T) {
	action := decideSwitchCompressAction(50000, 200000)
	assert.Equal(t, SwitchActionNoop, action,
		"50k conversation switched into a 200k window must be a no-op")

	action = decideSwitchCompressAction(50000, 50000)
	assert.Equal(t, SwitchActionNoop, action,
		"equal window and conversation size must be a no-op")
}

// TestSwitchTimeCompress_EmptySession_NoOp verifies that switching on an
// empty session never triggers re-window regardless of window size.
//
// BDD-15, BDD-29: Old=200k, new=8k, conv=0 -> no re-window (empty).
func TestSwitchTimeCompress_EmptySession_NoOp(t *testing.T) {
	action := decideSwitchCompressAction(0, 8000)
	assert.Equal(t, SwitchActionNoop, action,
		"empty session must never trigger re-window")
}

// TestSwitchTimeCompress_BoundaryEqualNoCompress confirms the boundary case
// where current conversation exactly equals new window does not trigger re-window.
func TestSwitchTimeCompress_BoundaryEqualNoCompress(t *testing.T) {
	action := decideSwitchCompressAction(8000, 8000)
	assert.Equal(t, SwitchActionNoop, action,
		"conversation equal to new window must not re-window (no overflow yet)")
}

// TestSwitchTimeCompress_SmallerToLarger_NoOp verifies switching to a model
// with more room never triggers re-window.
func TestSwitchTimeCompress_SmallerToLarger_NoOp(t *testing.T) {
	action := decideSwitchCompressAction(7000, 200000)
	assert.Equal(t, SwitchActionNoop, action,
		"switching to a larger-window model must not trigger re-window")
}

// --- handleModelSwitch integration tests (windowTrim behavior) ---

// TestSwitchTime_EndToEnd_HappyPath verifies the full integration: when an
// incoming message carries a model_name metadata that differs from the current
// agent model and the conversation is over-budget for the new model:
//
//  1. handleModelSwitch detects the switch,
//  2. calls windowTrim (NOT summarizeDroppedTurns — no LLM call),
//  3. the live window fits the new model's budget,
//  4. NO summary marker is written,
//  5. agent.Model is updated to the new model.
func TestSwitchTime_EndToEnd_HappyPath(t *testing.T) {
	const newModel = "openrouter/some-small-model"
	al, _, cleanup := newSwitchTestAgentLoop(t, "test-model", newModel)
	defer cleanup()

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	// Calibrated window: large enough that tool defs leave room for some history
	// turns (toolDefsTokens ≈ 5876 in the test environment), yet small enough
	// that the seeded conversation overflows and a trim fires.
	//
	// cw=20000, mt=4096 → budget = 20000 - 4096 - 1000 (5% headroom) = 14904.
	// toolDefsTokens ≈ 5876 → history budget ≈ 9028 tokens.
	// We seed 12 turns × ~1000 tok/turn = ~12000 tokens of history → overflow.
	// After trim the last 9 turns (~9000 tokens) fit within the history budget.
	agent.ContextWindow = 20000
	agent.MaxTokens = 4096
	al.mu.Lock()
	al.cfg.Agents.Defaults.ContextWindow = 20000
	al.mu.Unlock()

	oldModel := agent.Model
	require.NotEmpty(t, oldModel, "test setup: default agent must have a model")

	const sessionKey = "switch-test-session"
	// ~2500 chars per message ≈ 1000 tokens per message → 2000 tokens per turn.
	turnText := strings.Repeat("a", 2500)
	var seedMsgs []providers.Message
	for i := 0; i < 12; i++ {
		seedMsgs = append(seedMsgs, providers.Message{Role: "user", Content: turnText})
		seedMsgs = append(seedMsgs, providers.Message{Role: "assistant", Content: turnText})
	}
	agent.Sessions.SetHistory(sessionKey, seedMsgs)
	require.NoError(t, agent.Sessions.Save(sessionKey))

	historyBefore := agent.Sessions.GetHistory(sessionKey)
	tokensBefore := estimateHistoryTokens(historyBefore)

	require.NotEqual(t, oldModel, newModel,
		"test invariant: new model must differ from current model")

	updatedAgent, err := al.handleModelSwitch(
		context.Background(),
		agent,
		sessionKey,
		newModel,
		bus.InboundMessage{
			Metadata: map[string]string{"model_name": newModel},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, updatedAgent)

	// agent.Model must be updated.
	assert.Equal(t, newModel, updatedAgent.Model,
		"agent.Model must be updated to the new model after a switch")

	// No summary marker written (windowTrim never writes one).
	sessionSummary := updatedAgent.Sessions.GetSummary(sessionKey)
	assert.NotContains(t, sessionSummary, "Emergency compression dropped",
		"windowTrim path MUST NOT write a compression summary marker")
	assert.NotContains(t, sessionSummary, "Conversation moved",
		"windowTrim path MUST NOT write a model-switch summary note")

	// History must have shrunk.
	history := updatedAgent.Sessions.GetHistory(sessionKey)
	tokensAfter := estimateHistoryTokens(history)
	assert.Less(t, tokensAfter, tokensBefore,
		"history token estimate must have shrunk after switch-time trim")

	// The full request (history + tool defs + maxTokens) must fit within cw.
	// windowTrim enforces: suffixTokens + toolDefsTokens + recallSpanTokens <=
	// budget, where budget = cw - maxTokens - ceil(0.05*cw).  We verify the
	// same constraint post-trim to confirm windowTrim did its job.
	toolDefsTokens := estimateToolDefsTokens(updatedAgent.Tools.ToProviderDefs())
	totalAfter := tokensAfter + toolDefsTokens + 4096 // 4096 = MaxTokens
	assert.LessOrEqual(t, totalAfter, 20000,
		"post-trim total (history+tool_defs+max_tokens) must fit within the new cw")

	// ApplyAgentModel swap: provider pool must be queryable for the new model.
	require.NotNil(t, updatedAgent.Provider,
		"agent.Provider must not be nil after ApplyAgentModel swap")
	pinnedProv := updatedAgent.GetProviderForCandidate(providers.FallbackCandidate{
		Provider: "openai",
		Model:    newModel,
	})
	require.NotNil(t, pinnedProv, "post-switch pool returned nil provider for 'openai'")
}

// TestSwitchTime_EmptySession_NoSyntheticMessage verifies that switching on an
// empty session does NOT insert any synthetic message or write a summary.
func TestSwitchTime_EmptySession_NoSyntheticMessage(t *testing.T) {
	const newModel = "openrouter/some-other-model"
	al, _, cleanup := newSwitchTestAgentLoop(t, "test-model", newModel)
	defer cleanup()

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	agent.ContextWindow = 8000
	agent.MaxTokens = 4096

	oldModel := agent.Model
	require.NotEqual(t, oldModel, newModel)

	const sessionKey = "empty-session"
	agent.Sessions.SetHistory(sessionKey, nil)
	require.NoError(t, agent.Sessions.Save(sessionKey))

	updatedAgent, err := al.handleModelSwitch(
		context.Background(),
		agent,
		sessionKey,
		newModel,
		bus.InboundMessage{
			Metadata: map[string]string{"model_name": newModel},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, updatedAgent)

	history := updatedAgent.Sessions.GetHistory(sessionKey)
	assert.Empty(t, history, "empty session switch must leave history empty")
	assert.Equal(t, newModel, updatedAgent.Model,
		"empty session switch still updates agent.Model")

	summary := updatedAgent.Sessions.GetSummary(sessionKey)
	assert.Empty(t, summary,
		"empty session switch must not write a session summary")
}

// TestSwitchTime_LLMHistoryHasNoSystemMessage verifies that after a model
// switch with trim, history contains no injected role=="system" message
// (the summary path is deleted; the breadcrumb is separate from session summary).
func TestSwitchTime_LLMHistoryHasNoSystemMessage(t *testing.T) {
	const newModel = "openrouter/some-small-model"
	al, _, cleanup := newSwitchTestAgentLoop(t, "test-model", newModel)
	defer cleanup()

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	agent.ContextWindow = 8000
	agent.MaxTokens = 4096
	al.mu.Lock()
	al.cfg.Agents.Defaults.ContextWindow = 8000
	al.mu.Unlock()

	const sessionKey = "llm-no-system-msg"
	bigText := strings.Repeat("b", 50000)
	agent.Sessions.SetHistory(sessionKey, []providers.Message{
		{Role: "user", Content: "I need help with: " + bigText},
		{Role: "assistant", Content: "Sure: " + bigText},
	})
	require.NoError(t, agent.Sessions.Save(sessionKey))

	updatedAgent, err := al.handleModelSwitch(
		context.Background(),
		agent,
		sessionKey,
		newModel,
		bus.InboundMessage{
			Metadata: map[string]string{"model_name": newModel},
		},
	)
	require.NoError(t, err)
	require.NotNil(t, updatedAgent)

	// Session summary must be empty (windowTrim writes no summary).
	summary := updatedAgent.Sessions.GetSummary(sessionKey)
	assert.Empty(t, summary,
		"windowTrim switch path must not populate session summary")

	// History must have no synthetic system message.
	history := updatedAgent.Sessions.GetHistory(sessionKey)
	for _, m := range history {
		assert.NotEqual(t, "system", m.Role,
			"no synthetic system message must be in history after windowTrim switch")
	}
}

// TestSwitchTime_UnknownModel_LogsWarn is the W4-4 regression guard for
// silent-failure-A CRITICAL #1: when a typo'd metadata.model_name arrives,
// the code MUST emit a WARN rather than silently continuing.
//
// BDD: typo'd metadata.model_name -> WARN with resolve_error emitted.
func TestSwitchTime_UnknownModel_LogsWarn(t *testing.T) {
	tmpDir := t.TempDir()
	logFile := tmpDir + "/switch-unknown-model.log"

	prevLevel := logger.GetLevel()
	logger.DisableConsole()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging: %v", err)
	}
	t.Cleanup(func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	})

	// Register default model only — typo model is intentionally absent.
	al, _, cleanup := newSwitchTestAgentLoop(t, "test-model")
	defer cleanup()

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)
	agent.ContextWindow = 8000

	al.mu.Lock()
	al.cfg.Agents.Defaults.ContextWindow = 200000
	al.mu.Unlock()

	const sessionKey = "unknown-model-test"
	const typoModel = "not-a-real-model-typo"
	agent.Sessions.SetHistory(sessionKey, nil)
	require.NoError(t, agent.Sessions.Save(sessionKey))

	// Drive the switch with the typo'd model name.
	_, switchErr := al.handleModelSwitch(
		context.Background(),
		agent,
		sessionKey,
		typoModel,
		bus.InboundMessage{
			Metadata: map[string]string{"model_name": typoModel},
		},
	)
	if switchErr != nil {
		t.Logf("handleModelSwitch returned error (expected for unknown model): %v", switchErr)
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", logFile, err)
	}
	logged := string(data)

	if !strings.Contains(logged, "handleModelSwitch: requested model did not resolve") {
		t.Errorf("log file missing the W4-4 WARN marker; got:\n%s", logged)
	}
	if !strings.Contains(logged, typoModel) {
		t.Errorf("log file missing the typo'd model name %q; got:\n%s", typoModel, logged)
	}
	if !strings.Contains(logged, agent.ID) {
		t.Errorf("log file missing the agent_id %q; got:\n%s", agent.ID, logged)
	}
	if !strings.Contains(logged, "resolve_error") {
		t.Errorf("log file missing the resolve_error field; got:\n%s", logged)
	}
	if !strings.Contains(logged, `"level":"warn"`) {
		t.Errorf("log file missing the warn level; got:\n%s", logged)
	}
}

// TestSwitchTime_UnknownModel_SurfacesToUserNotJustLog is the regression test
// for item C: previously, when the caller's requested metadata.model_name
// could not be resolved, runTurn logged a backend-only WARN
// ("switch-time compress failed; continuing with current model") and
// silently continued the turn on the agent's current model — nothing else
// told the caller their model selection was ignored. This is the exact
// "picking a model has no effect" failure mode.
//
// Unlike TestSwitchTime_UnknownModel_LogsWarn (which drives handleModelSwitch
// directly and only asserts the WARN log), this test drives a FULL turn via
// runAgentLoop so it exercises runTurn's caller-side handling of a failed
// switch — the WARN log is necessary but not sufficient; the fix ALSO must:
//  1. write a Status="error" system entry to the JSONL transcript
//     (appendErrorTranscript, mirroring FR-001/FR-002's existing
//     rate-limit/provider-error pattern) so a session reopen shows it, and
//  2. push a live agent.EventKindNotification so the CURRENT session learns
//     immediately, without waiting for a reload.
//
// It also asserts the turn itself completes successfully on the OLD model
// (non-fatal — the user still gets a reply) rather than failing the whole
// turn.
func TestSwitchTime_UnknownModel_SurfacesToUserNotJustLog(t *testing.T) {
	al, _, cleanup := newSwitchTestAgentLoop(t, "test-model")
	defer cleanup()

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)
	oldModel := agent.Model
	require.NotEmpty(t, oldModel, "test setup: default agent must have a model")

	const typoModel = "not-a-real-model-typo"
	require.NotEqual(t, oldModel, typoModel, "test invariant: requested model must differ from current model")

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "web", agent.ID)
	require.NoError(t, err)
	sessionID := meta.ID

	// Subscribe to the event bus BEFORE the turn runs so we don't race the emit.
	sub := al.eventBus.Subscribe(8)
	defer al.eventBus.Unsubscribe(sub.ID)

	ctx := context.Background()
	result, runErr := al.runAgentLoop(ctx, agent, processOptions{
		SessionKey:          "switch-unknown-model-turn",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "hello",
		DefaultResponse:     defaultResponse,
		EnableSummary:       false,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
		Metadata:            map[string]string{"model_name": typoModel},
	})

	// Non-fatal: the turn must still complete (on the old model) and hand
	// back the mock provider's reply — a caller-side resolution failure must
	// not fail the whole turn.
	require.NoError(t, runErr, "a failed model switch must not fail the turn itself")
	assert.Equal(t, "Mock response", result, "turn must complete using mockProvider's reply on the OLD model")

	// --- 1. Transcript: a Status="error" system entry must exist and name
	//        both the requested (unresolvable) model and the model actually used.
	entries := readTranscriptEntries(t, store, sessionID)
	sysEntries := findSystemEntries(entries)
	var found *session.TranscriptEntry
	for i := range sysEntries {
		if sysEntries[i].Status == "error" && strings.Contains(sysEntries[i].Content, typoModel) {
			cp := sysEntries[i]
			found = &cp
			break
		}
	}
	require.NotNil(t, found,
		"transcript must contain a Status=\"error\" system entry naming the unresolvable "+
			"model %q so a session reopen still shows why the switch was ignored; entries: %+v",
		typoModel, sysEntries)
	assert.Contains(t, found.Content, oldModel,
		"the error entry should also name the model the turn actually used, so the user "+
			"understands what happened")

	// --- 2. Live signal: an EventKindError (persistence-companion, FR-002
	//        pattern) must have been emitted for this failure. (No notification
	//        frame: `model_switch_failed` is not a contract NotificationFrame
	//        notification_type, so the SPA would drop it — the error event +
	//        the transcript entry above are the surfacing.)
	var sawError bool
	drain := time.After(2 * time.Second)
	for !sawError {
		select {
		case evt := <-sub.C:
			if evt.Kind == EventKindError {
				if p, ok := evt.Payload.(ErrorPayload); ok && p.Stage == "model_switch" {
					sawError = true
					assert.Contains(t, p.Message, typoModel)
				}
			}
		case <-drain:
			t.Fatalf("timed out waiting for EventKindError(model_switch)")
		}
	}
}

// Compile-time check: failingProvider satisfies LLMProvider.
var _ providers.LLMProvider = (*failingProvider)(nil)

// failingProvider returns an error from Chat. Kept for interface compatibility;
// the summarizeDroppedTurns path is deleted and no longer needs it.
type failingProvider struct{}

func (f *failingProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return nil, context.DeadlineExceeded
}

func (f *failingProvider) GetDefaultModel() string {
	return "failing-model"
}

// Reference unused import session to avoid compile error if the file is later trimmed.
var _ session.UnifiedSessionType = session.SessionTypeChat
