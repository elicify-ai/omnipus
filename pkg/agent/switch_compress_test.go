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
// Each row is the exact pair (openai, <name>) — a catalog provider id and a
// BARE model id (ADR-067 FR-034). The model ids are invented, which is fine:
// a row resolves through what IT serves, so the pair is addressable even
// though the catalog lists no such model under openai.
//
// The stub base URL is a NON-loopback, unresolvable host on purpose: this
// "openai" row is not in the (empty) test catalog, so ADR-066's resolver
// classifies it through ADR-067's custom-row locality predicate — a
// loopback host would make it `locality: local`, and a local endpoint with
// no reported window is refused at turn start (context_window_unknown,
// D3) rather than floored. These tests model a CLOUD provider whose window
// nobody knows (→ the 128k floor), so the host must read as public.
func newSwitchTestAgentLoop(t *testing.T, models ...string) (al *AgentLoop, cfg *config.Config, cleanup func()) {
	t.Helper()
	tmpDir := t.TempDir()
	t.Setenv("SWITCH_TEST_KEY", "switch-test-key")
	mkProvider := func(name string) *config.ModelConfig {
		return &config.ModelConfig{
			Provider:  "openai",
			Model:     name,
			APIBase:   "http://openai-stub.invalid:1",
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
				DefaultModel:      config.DefaultModel{Provider: "openai", Model: "test-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
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

	// --- Fixture calibration (derived, NOT hardcoded) -----------------------
	//
	// The builtin tool catalog is sent on EVERY request, and it grows as tools
	// are added: it cost 13347 estimated tokens before the ADR-055/056 epic and
	// 16059 after plan_correct + stop_plan + list_jobs landed. A hardcoded
	// context window therefore ROTS. This fixture used to pin cw=20000 on the
	// (by then already stale) assumption "toolDefsTokens ≈ 5876"; once
	// toolDefs + maxTokens alone exceeded 20000 the scenario became
	// unsatisfiable — NO amount of eviction can make the request fit, so
	// windowTrim correctly bottomed out on its FR-003 last-user-Turn floor and
	// the post-trim assertion below could never hold.
	//
	// Derive the window from the MEASURED catalog cost instead, so this test
	// keeps asserting windowTrim's BEHAVIOUR (it evicts until the request fits)
	// rather than a constant that silently expires the next time a tool lands.
	//
	// windowTrim keeps the largest suffix window[b:] satisfying
	//   suffix + toolDefs <= cw - maxTokens - ceil(0.05*cw) - pinnedCore
	// so the smallest cw leaving room for keepTurns turns of history solves
	//   0.95*cw >= maxTokens + pinnedCore + toolDefs + keepTurns*turnTokens.
	const maxTokens = 4096
	const keepTurns = 3

	toolDefsTokens := estimateToolDefsTokens(agent.Tools.ToProviderDefs())
	pinnedCore := breadcrumbTokenCap
	if agent.ContextBuilder != nil {
		// Same chars*2/5 heuristic windowTrim uses for the system prompt.
		pinnedCore += len(agent.ContextBuilder.BuildSystemPromptWithCache()) * 2 / 5
	}

	// ~2500 chars per message ≈ 1000 tokens per message → ~2000 tokens per turn.
	turnText := strings.Repeat("a", 2500)
	turnTokens := 2 * estimateMessageTokens(providers.Message{Role: "user", Content: turnText})
	require.Positive(t, turnTokens, "test setup: seeded turn must cost tokens")

	// +256 slack absorbs the integer rounding in the *20/19 solve and the
	// ceil() in the headroom term; it stays well below one turn (turnTokens),
	// so the trim still lands on exactly keepTurns turns.
	contextWindow := (maxTokens+pinnedCore+toolDefsTokens+keepTurns*turnTokens)*20/19 + 256

	// Guard the scenario is actually exercisable. If this ever fails, the
	// builtin catalog has grown enough that the derivation above no longer
	// leaves room for keepTurns turns — fix the catalog or the derivation,
	// do not paper over it.
	require.Greater(t, contextWindow, toolDefsTokens+maxTokens+keepTurns*turnTokens,
		"derived context window must leave real room for history after tool defs "+
			"(tool_defs=%d, max_tokens=%d) — the builtin catalog has outgrown this fixture",
		toolDefsTokens, maxTokens)

	agent.ContextWindow = contextWindow
	agent.MaxTokens = maxTokens
	al.mu.Lock()
	al.cfg.Context.DefaultContextWindow = intPtr(contextWindow)
	al.mu.Unlock()

	oldModel := agent.Model
	require.NotEmpty(t, oldModel, "test setup: default agent must have a model")

	const sessionKey = "switch-test-session"
	// Seed enough turns that the conversation overflows the new window, so
	// decideSwitchCompressAction returns Compress and a trim actually fires.
	seedTurns := contextWindow/turnTokens + 4
	var seedMsgs []providers.Message
	for i := 0; i < seedTurns; i++ {
		seedMsgs = append(seedMsgs, providers.Message{Role: "user", Content: turnText})
		seedMsgs = append(seedMsgs, providers.Message{Role: "assistant", Content: turnText})
	}
	agent.Sessions.SetHistory(sessionKey, seedMsgs)
	require.NoError(t, agent.Sessions.Save(sessionKey))

	historyBefore := agent.Sessions.GetHistory(sessionKey)
	tokensBefore := estimateHistoryTokens(historyBefore)
	require.Greater(t, tokensBefore, contextWindow,
		"test setup: seeded conversation must overflow the new window so "+
			"decideSwitchCompressAction returns Compress")

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

	// History must have shrunk.
	history := updatedAgent.Sessions.GetHistory(sessionKey)

	// No marker injected (windowTrim never synthesizes one).
	postSwitch := joinWindowContent(history)
	assert.NotContains(t, postSwitch, "Emergency compression dropped",
		"windowTrim path MUST NOT inject a compression marker")
	assert.NotContains(t, postSwitch, "Conversation moved",
		"windowTrim path MUST NOT inject a model-switch note")

	tokensAfter := estimateHistoryTokens(history)
	assert.Less(t, tokensAfter, tokensBefore,
		"history token estimate must have shrunk after switch-time trim")

	// windowTrim must have taken the NORMAL Turn-boundary path, not the FR-003
	// emergency floor (which keeps only the most-recent user Turn and
	// terminates even when the result is still over budget). Bottoming out on
	// the floor is how this test used to "trim" while still failing the budget
	// assertion below, so assert the distinction explicitly.
	assert.Greater(t, len(history), 2,
		"windowTrim must cut at a real Turn boundary, not bottom out on the "+
			"FR-003 last-user-Turn floor (floor keeps 2 msgs)")

	// The full request (history + tool defs + maxTokens) must fit within cw.
	// windowTrim enforces: suffixTokens + toolDefsTokens + recallSpanTokens <=
	// budget, where budget = cw - maxTokens - ceil(0.05*cw) - pinnedCore.  We
	// verify the looser, user-visible constraint post-trim — deliberately NOT
	// windowTrim's internal formula — to confirm windowTrim did its job.
	postToolDefsTokens := estimateToolDefsTokens(updatedAgent.Tools.ToProviderDefs())
	totalAfter := tokensAfter + postToolDefsTokens + maxTokens
	assert.LessOrEqual(t, totalAfter, contextWindow,
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
}

// TestSwitchTime_LLMHistoryHasNoSystemMessage verifies that after a model
// switch with trim, history contains no injected role=="system" message
// (the summary path is deleted; the breadcrumb is separate from history).
func TestSwitchTime_LLMHistoryHasNoSystemMessage(t *testing.T) {
	const newModel = "openrouter/some-small-model"
	al, _, cleanup := newSwitchTestAgentLoop(t, "test-model", newModel)
	defer cleanup()

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)

	agent.ContextWindow = 8000
	agent.MaxTokens = 4096
	al.mu.Lock()
	al.cfg.Context.DefaultContextWindow = intPtr(8000)
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
	al.cfg.Context.DefaultContextWindow = intPtr(200000)
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

	// --- 1. Transcript: a Status="error" system entry must exist. The
	//        BLOCK 2 sanitizer removes the model-name identity from the
	//        persisted content; the entry carries the generic internal-error
	//        copy and the typed code, NOT the raw unresolvable model slug.
	entries := readTranscriptEntries(t, store, sessionID)
	sysEntries := findSystemEntries(entries)
	var found *session.TranscriptEntry
	for i := range sysEntries {
		if sysEntries[i].Status == "error" &&
			!strings.Contains(sysEntries[i].Content, typoModel) &&
			!strings.Contains(sysEntries[i].Content, "Model") {
			cp := sysEntries[i]
			found = &cp
			break
		}
	}
	require.NotNil(t, found,
		"transcript must contain a Status=\"error\" system entry with the generic "+
			"internal-error copy (no model-name identity); entries: %+v", sysEntries)
	// BLOCK 2 / model_switch invariant: the entry must NOT contain the
	// unresolvable model slug OR the actually-used model. The generic copy
	// describes the situation without exposing the model name.
	if strings.Contains(found.Content, typoModel) {
		t.Fatalf("transcript must NOT contain the unresolvable model %q; entry: %q",
			typoModel, found.Content)
	}
	if strings.Contains(found.Content, oldModel) {
		t.Fatalf("transcript must NOT contain the actually-used model %q; entry: %q",
			oldModel, found.Content)
	}

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
					if strings.Contains(p.Message, typoModel) {
						t.Fatalf("live model_switch error payload must NOT contain the unresolvable model %q; got %q",
							typoModel, p.Message)
					}
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

// TestApplyAgentModel_MaxTokensDoesNotRatchetDown pins FR-005b's clamp as a
// FUNCTION of the configured value, not a running minimum.
//
// clampMaxTokensForWindow only ever lowers. ApplyAgentModel used to feed the
// CURRENT (possibly already-clamped) agent.MaxTokens back into it, so the
// field was monotonically decreasing for the lifetime of the process: a
// round-trip through a small-window model left the agent permanently capped
// at that model's window/4 — answers silently truncated on a 200k model, with
// no log line and no recovery short of a gateway restart.
func TestApplyAgentModel_MaxTokensDoesNotRatchetDown(t *testing.T) {
	const (
		bigModel   = "test-model"
		smallModel = "openrouter/small-window-model"
	)
	al, cfg, cleanup := newSwitchTestAgentLoop(t, bigModel, smallModel)
	defer cleanup()

	inst := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, inst)
	configured := inst.MaxTokens
	require.Positive(t, configured)

	// Rung 3 (the global default) is the window every ResolveWindow answers
	// with here, so switching models is what changes the window.
	setDefaultWindow := func(w int) {
		al.mu.Lock()
		al.cfg.Context.DefaultContextWindow = intPtr(w)
		al.mu.Unlock()
		cfg.Context.DefaultContextWindow = intPtr(w)
	}

	// Down to a window small enough that B would go non-positive: the clamp
	// fires and MaxTokens drops to window/4.
	setDefaultWindow(5000)
	_, err := al.ApplyAgentModel(inst.ID, smallModel)
	require.NoError(t, err)
	clamped := inst.MaxTokens
	require.Less(t, clamped, configured, "precondition: the small window clamps max_tokens down")
	assert.Equal(t, 5000/4, clamped)

	// Back up to a large window: the clamp must not fire, and the CONFIGURED
	// value must come back.
	setDefaultWindow(200_000)
	_, err = al.ApplyAgentModel(inst.ID, bigModel)
	require.NoError(t, err)

	assert.Equal(t, configured, inst.MaxTokens,
		"switching back to a large-window model must restore the configured max_tokens — "+
			"clamping the already-clamped value ratchets it down permanently")
}
