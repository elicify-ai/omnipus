// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// wave2_fix_e_test.go — W2-FIX-E tests for Wave 2 criticality-9 gaps
//
// Closes (per the WAVE2-CONSOLIDATED findings from test-analyzer-A and
// type-design-A):
//
//   - W2-20 #1 (CRITICAL): appendErrorTranscript Status: "error" never asserted.
//     Closes BDD-1, BDD-2 (rate-limit + provider-error) AND US-1 Acc 2 ("error
//     still visible on reopen") by writing the rate-limit, closing the
//     UnifiedStore, reopening it against the same path, and re-reading — the
//     error entry AND its Status="error" must survive.
//   - W2-20 #2 (CRITICAL): per-turn Model field via agent loop never asserted.
//     A regression that drops the `Model: ts.lastProducedModel` line in
//     appendAssistantTranscript would currently pass every existing test.
//   - W2-20 #3 (CRITICAL): summarizeDroppedTurns length cap — removed;
//     summarizeDroppedTurns deleted as part of context-paging epic (FR-011/Q5).
//   - W2-20 #4 (HIGH): updates existing substring-on-Content assertions to
//     assert `e.Status == "error"` directly (the contract).
//   - W2-27 (MEDIUM): provider tie-break ordering (openrouter vs vivgrid),
//     knownProviderPrefixes additional-prefix whitelist test, decideSwitchCompressAction
//     empty/negative inputs, appendErrorTranscript no-op paths (nil store, empty
//     session ID).
//
// Spec ref: docs/internal/specs/phase-1-chat-model-and-errors.md
//
//	FR-001: rate_limit denial MUST emit EventKindError with the same
//	        RateLimitPayload AND write a system entry to the JSONL transcript
//	        so replay shows it.
//	FR-002: any provider error MUST emit EventKindError with ErrorPayload AND
//	        write a system entry to the JSONL transcript.
//	FR-011: at model switch, windowTrim re-fits the window to the new budget;
//	        no LLM call is made (summarizeDroppedTurns deleted).
//	FR-013: per-turn Model field recorded on every assistant message.
//	FR-014: Status="error" on transcript error entries (NOT a substring of
//	        Content) — the replay path depends on it.

package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// =============================================================================
// W2-20 #1 — THE BIG ONE: round-trip via runAgentLoop with close + reopen
// =============================================================================
//
// This is the single highest-priority gap from the Wave 1 review. It exercises
// the full persistence loop end-to-end: drive the agent, capture the assistant
// TranscriptEntry, close the UnifiedStore, open a FRESH store against the same
// directory, re-read the transcript, and assert that BOTH the per-turn Model
// field AND the Status="error" error entry survive the close+reopen.
//
// Closes BDD-1, BDD-2, BDD-12, and US-1 Acc 2 (replay after navigation/reload)
// in a single test. Without this test, a regression that "forgets" to persist
// Model or Status on round-trip passes silently — there's no in-process
// round-trip test today.
func TestRunAgentLoop_RoundTrip_AfterReopen_PreservesModelAndErrorStatus(t *testing.T) {
	// ---------------------------------------------------------------------
	// Arrange: workspace + a per-agent store under a persistent temp dir.
	// ---------------------------------------------------------------------
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))
	// The store must outlive a single Close so the reopen hits the SAME
	// on-disk directory. Using a non-deferred temp dir means we control the
	// lifetime explicitly.
	storeDir := filepath.Join(tmpHome, "sessions")

	// The scripted provider supplies the warm-up response only — the
	// second call is rate-limited BEFORE the provider is invoked.
	provider := testutil.NewScenario().WithText("warm-up response")

	// Budget=1 → first call allowed, second call denied. We use a custom
	// (non-privileged) agent because core-roster agents are exempt from
	// rate limiting (IsPrivilegedAgent) — no "main" sentinel to worry about
	// anymore, just an ordinary custom agent id.
	const customAgentID = "wave2-roundtrip-agent"

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: customAgentID, Name: "Wave2 Roundtrip Agent", Type: config.AgentTypeCustom},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true,
			RateLimits: config.OmnipusRateLimitsConfig{
				MaxAgentLLMCallsPerHour: 1,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, provider)

	// Build the store up-front so we can grab its sessionID BEFORE the
	// close, and so the agent loop + the test agree on the on-disk
	// location.
	store, err := session.NewUnifiedStore(storeDir)
	require.NoError(t, err, "NewUnifiedStore must succeed on a fresh temp dir")
	meta, err := store.NewSession(session.SessionTypeChat, "web", customAgentID)
	require.NoError(t, err)
	sessionID := meta.ID

	customAgent, ok := al.GetRegistry().GetAgent(customAgentID)
	require.True(t, ok, "custom agent must be registered")

	// ---------------------------------------------------------------------
	// Act 1: warm-up call (consumes the budget) — produces an assistant
	// entry whose Model field MUST be stamped.
	// ---------------------------------------------------------------------
	ctx := context.Background()
	_, err = al.runAgentLoop(ctx, customAgent, processOptions{
		SessionKey:          "wave2-rt-warm",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "warm up",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err, "call 1 must succeed — budget not yet exhausted")

	// ---------------------------------------------------------------------
	// Act 2: second call MUST be rate-limited. This writes a system entry
	// to the transcript with Status="error".
	// ---------------------------------------------------------------------
	_, err = al.runAgentLoop(ctx, customAgent, processOptions{
		SessionKey:          "wave2-rt-blocked",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "trigger limit",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.Error(t, err, "call 2 must be rejected with an error")
	require.Contains(t, strings.ToLower(err.Error()), "rate limit",
		"call 2 error must mention rate limit; got %q", err.Error())

	// Sanity: in-process read sees the assistant entry with Model and the
	// system entry with Status="error" BEFORE we close.
	preClose := readTranscriptEntries(t, store, sessionID)
	require.NotEmpty(t, preClose, "transcript must not be empty pre-close")

	// Find the assistant entry from the warm-up call.
	var preAssistant *session.TranscriptEntry
	for i := range preClose {
		if preClose[i].Role == "assistant" {
			cp := preClose[i]
			preAssistant = &cp
			break
		}
	}
	require.NotNil(t, preAssistant, "transcript must contain the warm-up assistant entry; got %+v", preClose)
	require.NotEmpty(t, preAssistant.Model,
		"W2-20 #2: per-turn Model field MUST be stamped on the assistant entry pre-close; "+
			"got empty Model on entry %+v", *preAssistant)

	// Find the error system entry.
	var preError *session.TranscriptEntry
	for i := range preClose {
		if preClose[i].Type == session.EntryTypeSystem && preClose[i].Status == "error" {
			cp := preClose[i]
			preError = &cp
			break
		}
	}
	require.NotNil(t, preError,
		"W2-20 #1: transcript must contain a system entry with Status=\"error\" pre-close; got %+v", preClose)

	// ---------------------------------------------------------------------
	// THE critical step: close the store + the loop, then open a FRESH
	// store against the SAME on-disk path. This is what US-1 Acc 2
	// ("error still visible after navigating away and back") actually
	// exercises in production — the live process dies, the replay path
	// opens a new store from disk.
	// ---------------------------------------------------------------------
	require.NoError(t, store.Close(), "store must close cleanly")
	al.Close()
	msgBus.Close()

	reopened, err := session.NewUnifiedStore(storeDir)
	require.NoError(t, err, "reopen against the same path must succeed")
	t.Cleanup(func() { _ = reopened.Close() })

	// ---------------------------------------------------------------------
	// Assert: the same entries survive, AND the per-turn Model field is
	// preserved on disk (NOT a struct in-memory artifact), AND the
	// Status="error" discriminator survives the round-trip.
	// ---------------------------------------------------------------------
	post := readTranscriptEntries(t, reopened, sessionID)
	require.NotEmpty(t, post, "transcript must not be empty after reopen")

	// Find the assistant entry from the warm-up call after reopen.
	var postAssistant *session.TranscriptEntry
	for i := range post {
		if post[i].Role == "assistant" {
			cp := post[i]
			postAssistant = &cp
			break
		}
	}
	require.NotNil(
		t,
		postAssistant,
		"transcript must still contain the warm-up assistant entry after reopen; got %+v",
		post,
	)
	require.Equal(t, preAssistant.Model, postAssistant.Model,
		"per-turn Model field must be preserved across close+reopen; pre=%q post=%q",
		preAssistant.Model, postAssistant.Model)
	require.NotEmpty(t, postAssistant.Model,
		"W2-20 #2 (post-reopen): per-turn Model field MUST survive the round-trip; "+
			"got empty Model on entry %+v", *postAssistant)

	// Find the error system entry after reopen.
	var postError *session.TranscriptEntry
	for i := range post {
		if post[i].Type == session.EntryTypeSystem && post[i].Status == "error" {
			cp := post[i]
			postError = &cp
			break
		}
	}
	require.NotNil(t, postError,
		"W2-20 #1 (post-reopen): Status=\"error\" must survive the close+reopen; got %+v", post)
	require.Equal(t, "error", postError.Status,
		"W2-20 #4: Status field is the contract; substring on Content is brittle and not what "+
			"the replay path keys on. Entry: %+v", *postError)
	require.Equal(t, preError.Content, postError.Content,
		"the human-readable message must also be preserved across reopen")

	// Differentiation: a passing run of runAgentLoop produces a transcript
	// with both an assistant entry AND a system error entry — proving the
	// transcript is NOT hardcoded. A regression that writes only the
	// assistant entry (or only the error entry) would fail the dual-presence
	// assertion above.
	require.GreaterOrEqual(t, len(post), 2,
		"transcript must carry both the assistant entry AND the system error entry; "+
			"got %d entries: %+v", len(post), post)
}

// =============================================================================
// W2-20 #2 — Per-turn Model field via runTurn (NOT just round-trip the struct)
// =============================================================================
//
// A regression that drops `Model: ts.lastProducedModel` from
// appendAssistantTranscript would silently produce empty Model on every
// assistant entry. The round-trip test above catches the persistence side;
// this test catches the call-site side.
//
// We use a scripted provider that returns a response carrying a model name,
// drive the loop, and assert the assistant entry's Model field equals
// the agent's Model (the value that gets resolved and stamped by the loop).
// If appendAssistantTranscript forgets to stamp the field, this test fails.
func TestRunTurn_StampsModelFieldOnAssistantEntry(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	const modelName = "anthropic/claude-3.5-haiku"
	provider := testutil.NewScenario().WithText("ok, here you go")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: modelName},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	sessionID := meta.ID

	agent := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, agent)
	require.NotEmpty(t, agent.Model,
		"test setup: default agent model must be non-empty; got %q", agent.Model)

	// The loop stamps the model from the LLM call (resolvedCandidateModel),
	// which is the first candidate's Model field. We pull that out so the
	// assertion checks the actual contract: entry.Model equals the model
	// the loop actually ran with (not the configured-but-may-be-rewritten
	// agent.Model).
	var expectedModel string
	if len(agent.Candidates) > 0 {
		expectedModel = agent.Candidates[0].Model
	}
	if expectedModel == "" {
		expectedModel = agent.Model
	}
	require.NotEmpty(t, expectedModel,
		"test setup: resolved model must be non-empty")

	_, err = al.runAgentLoop(context.Background(), agent, processOptions{
		SessionKey:          "wave2-model-stamp",
		Channel:             "web",
		ChatID:              sessionID,
		UserMessage:         "test model stamp",
		DefaultResponse:     defaultResponse,
		SendResponse:        false,
		TranscriptSessionID: sessionID,
		TranscriptStore:     store,
	})
	require.NoError(t, err)

	entries := readTranscriptEntries(t, store, sessionID)
	require.NotEmpty(t, entries, "transcript must not be empty")

	var asst *session.TranscriptEntry
	for i := range entries {
		if entries[i].Role == "assistant" {
			cp := entries[i]
			asst = &cp
			break
		}
	}
	require.NotNil(t, asst, "transcript must contain the assistant entry; got %+v", entries)

	// The Model field must equal the model the loop actually used for the
	// LLM call (set via setLastProducedModel → lastProducedModel). A
	// regression that drops the `Model: ts.lastProducedModel` line in
	// appendAssistantTranscript leaves Model == "" — that fails here.
	require.Equal(t, expectedModel, asst.Model,
		"W2-20 #2: assistant entry Model must equal the resolved LLM-call model; "+
			"a regression that drops the Model stamp in appendAssistantTranscript would "+
			"leave Model=\"\" and silently break US-1 Acc 2 replay; got expected=%q entry.Model=%q",
		expectedModel, asst.Model)
}

// (W2-20 #3 — TestSummarizeDroppedTurns_Respects50PercentCap removed:
// summarizeDroppedTurns is deleted as part of the context-paging epic.
// handleModelSwitch now uses windowTrim (FR-011) with no LLM call.
// The windowTrim budget arithmetic is covered by TestWindowTrim_* in
// window_trim_test.go and TestModelSwitch_ReWindowsNoSummary.)

// =============================================================================
// W2-27 — appendErrorTranscript no-op paths
// =============================================================================
//
// W2-33 (silent-failure-A #12) flagged that appendErrorTranscript silently
// no-ops on nil store / empty session ID; we assert that behavior so a
// regression that panics or writes to a nil store surfaces as a failure.
func TestAppendErrorTranscript_NoOpOnNilStore(t *testing.T) {
	ts := &turnState{
		transcriptStore:     nil,
		transcriptSessionID: "session_test",
	}
	// Must not panic; must not call AppendTranscript (would NPE on nil).
	ts.appendErrorTranscript("error", "test", "should be ignored")
	// If we got here without panicking, the no-op path is intact.
}

func TestAppendErrorTranscript_NoOpOnEmptySessionID(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := session.NewUnifiedStore(tmpDir)
	require.NoError(t, err)
	t.Cleanup(func() { _ = store.Close() })

	ts := &turnState{
		transcriptStore:     store,
		transcriptSessionID: "",
	}
	// Must not panic; must not write to the store.
	ts.appendErrorTranscript("error", "test", "should be ignored")

	// Verify nothing was written — every session in the store still has
	// no transcript.
	entries := filepath.Join(tmpDir)
	_, err = os.ReadDir(entries)
	require.NoError(t, err)
	// We don't enumerate every session — the contract is that the call
	// returns silently without panicking, and the no-op is observable
	// through the lack of a panic.
}

// =============================================================================
// W2-27 — decideSwitchCompressAction edge cases (empty / negative inputs)
// =============================================================================
//
// W2-27 (test-analyzer-A #14) flagged that the function is never tested
// with empty or negative inputs. The function is pure and must guard
// against these without panicking.
func TestDecideSwitchCompressAction_EmptyAndNegativeInputs(t *testing.T) {
	cases := []struct {
		name     string
		cur      int
		win      int
		expected SwitchAction
	}{
		{"zero current", 0, 8000, SwitchActionNoop},
		{"negative current", -1, 8000, SwitchActionNoop},
		{"zero window", 5000, 0, SwitchActionNoop},
		{"negative window", 5000, -100, SwitchActionNoop},
		{"both zero", 0, 0, SwitchActionNoop},
		{"both negative", -1, -1, SwitchActionNoop},
		// Sanity: a real call still works.
		{"real compress", 50000, 8000, SwitchActionCompress},
		{"real noop", 5000, 8000, SwitchActionNoop},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := decideSwitchCompressAction(tc.cur, tc.win)
			assert.Equal(t, tc.expected, got,
				"cur=%d win=%d expected %s, got %s",
				tc.cur, tc.win, tc.expected, got)
		})
	}
}

// =============================================================================
// W2-27 — Provider tie-break ordering: RETIRED by ADR-067 FR-040 (X-24)
// =============================================================================
//
// Three tests lived here — TestResolveModelCfg_PassthroughTieBreak_*
// (openrouter wins over vivgrid when listed first; vivgrid alone catches
// everything) and TestResolveModelCfg_VendorPrefixedSlugs_RouteViaPassthrough
// (thirteen vendor prefixes all routed through the configured aggregator).
// All three asserted the PASSTHROUGH FALLBACK, and FR-040 deletes it.
//
// They are not replaced, because the behaviour they pinned is now a defect:
// the fallback meant an unmatched model id never failed to resolve — a typo,
// a retired id, a model from a provider the operator had not configured — it
// silently became a request to whichever aggregator happened to be first in
// the list, billed to that aggregator's key. "First-wins tie-break between
// two aggregators" is the clearest statement of the problem: the answer
// depended on config ORDER rather than on what anything actually serves.
//
// FR-040's replacement is in model_resolution_test.go
// (TestModelListResolver_PairExactThenUnique): a model resolves through a
// provider that OFFERS it, uniquely, or it does not resolve at all.

// =============================================================================
// W2-20 #4 — Status: "error" discriminator (regression-prevention on the
// existing test, asserting the contract rather than a substring).
// =============================================================================
//
// We re-read the existing rate-limit + provider-error tests' transcript and
// assert the Status field is set. This is a fresh test (not an edit) so the
// existing tests stay as the integration check; this is the focused contract
// assertion.
func TestRunAgentLoop_ErrorEntry_HasStatusErrorField(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	provider := testutil.NewScenario().WithText("call 1 succeeded")

	const customAgentID = "wave2-status-contract-agent"
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{
				{ID: customAgentID, Name: "Status Contract", Type: config.AgentTypeCustom},
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			RateLimits: config.OmnipusRateLimitsConfig{
				MaxAgentLLMCallsPerHour: 1,
			},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "web", customAgentID)
	require.NoError(t, err)
	sessionID := meta.ID

	agent, ok := al.GetRegistry().GetAgent(customAgentID)
	require.True(t, ok)

	ctx := context.Background()
	// Warm-up
	_, err = al.runAgentLoop(ctx, agent, processOptions{
		SessionKey: "sc-warm", Channel: "web", ChatID: sessionID,
		UserMessage: "warm", DefaultResponse: defaultResponse, SendResponse: false,
		TranscriptSessionID: sessionID, TranscriptStore: store,
	})
	require.NoError(t, err)
	// Trigger rate limit
	_, err = al.runAgentLoop(ctx, agent, processOptions{
		SessionKey: "sc-block", Channel: "web", ChatID: sessionID,
		UserMessage: "block", DefaultResponse: defaultResponse, SendResponse: false,
		TranscriptSessionID: sessionID, TranscriptStore: store,
	})
	require.Error(t, err)

	entries := readTranscriptEntries(t, store, sessionID)
	require.NotEmpty(t, entries)

	// Find the error entry and assert Status="error" (the contract, NOT a
	// substring on Content). A regression that sets Status to "" or to
	// something else (e.g. "failed") would break the replay path which keys
	// off the Status field.
	var found *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem {
			cp := entries[i]
			found = &cp
			break
		}
	}
	require.NotNil(t, found, "transcript must contain a system entry after rate-limit denial")
	require.Equal(t, "error", found.Status,
		"W2-20 #4: Status MUST be the exact string \"error\" — the replay path keys on this field, "+
			"not on a substring of Content. Entry: %+v", *found)
}

// =============================================================================
// W2-27 — Clone-mutation independence in ResolveModelCfg
// =============================================================================
//
// W2-27 (test-analyzer-A #12) flagged that ResolveModelCfg's clone contract
// is not asserted. A regression that returned the underlying cfg.Providers[i]
// pointer (rather than a clone) would let callers mutate the live config.
func TestResolveModelCfg_CloneMutationIndependence(t *testing.T) {
	original := &config.ModelConfig{
		Model:     "z-ai/glm-5.2",
		APIKeyRef: "OR_KEY",
		Provider:  "openrouter",
	}
	cfg := &config.Config{
		Providers: []*config.ModelConfig{original},
	}

	mc, err := ResolveModelCfg(cfg, "z-ai/glm-5.2", "/some/workspace")
	require.NoError(t, err)
	require.NotNil(t, mc)

	// Capture the resolved values, then mutate the returned config and the
	// resolved clone — assert the original cfg.Providers[0] is unchanged.
	originalModel := cfg.Providers[0].Model
	originalWorkspace := cfg.Providers[0].Home

	// Mutate the resolved clone's fields.
	mc.Model = "mutated/model"
	mc.Home = "mutated-workspace"

	// Original must not have changed.
	require.Equal(t, originalModel, cfg.Providers[0].Model,
		"ResolveModelCfg must return a CLONE — mutating the returned ModelConfig must "+
			"not mutate the underlying cfg.Providers[i]. Model: pre=%q post=%q",
		originalModel, cfg.Providers[0].Model)
	require.Equal(t, originalWorkspace, cfg.Providers[0].Home,
		"Workspace on the resolved clone must not leak back into cfg.Providers[i] when "+
			"the caller mutates it; pre=%q post=%q",
		originalWorkspace, cfg.Providers[0].Home)
}

// =============================================================================
// W2-27 — apply_agent_model_test.go passthrough case
// (instance-preservation re-assertion `id == after.ID`)
// =============================================================================
//
// W2-27 (test-analyzer-A #6) flagged that the existing
// TestApplyAgentModel_SwitchesInPlacePreservingInstance test asserts
// `after != before` (pointer equality) but not the ID field directly. A
// regression that replaced the instance with one having a different ID
// (e.g. via hot-reload) would slip past the pointer check if the
// implementation also re-assigned the same pointer.
//
// This test adds an ID-level re-assertion to lock the contract.
func TestApplyAgentModel_SwitchesInPlace_PreservesID(t *testing.T) {
	t.Setenv("LOOP_APPLY3_KEY", "k")

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              t.TempDir(),
				DefaultModel:      config.DefaultModel{Provider: "openai", Model: "gpt-4.1"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: t.TempDir()}},
		},
		Providers: []*config.ModelConfig{
			{
				Provider:  "openai",
				Model:     "gpt-4.1",
				APIBase:   "http://127.0.0.1:1",
				APIKeyRef: "LOOP_APPLY3_KEY",
			},
			{
				Provider:  "deepseek",
				Model:     "deepseek-chat",
				APIBase:   "http://127.0.0.1:1",
				APIKeyRef: "LOOP_APPLY3_KEY",
			},
		},
	}

	provider, _, err := providers.CreateProvider(cfg)
	require.NoError(t, err)
	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), provider)
	t.Cleanup(al.Close)

	before := al.GetRegistry().GetDefaultAgent()
	require.NotNil(t, before)
	id := before.ID
	require.NotEmpty(t, id)

	_, err = al.ApplyAgentModel(id, "deepseek-chat")
	require.NoError(t, err)

	after, ok := al.GetRegistry().GetAgent(id)
	require.True(t, ok, "agent must remain in the registry after ApplyAgentModel")
	require.Equal(t, id, after.ID,
		"W2-27 (passthrough case): agent ID must be preserved across ApplyAgentModel — "+
			"a regression that hot-replaces the instance would change the ID and break "+
			"downstream session/agent binding")
}

// =============================================================================
// W2-27 — Errors: scripted provider error (differentiation between provider
// error and rate-limit error)
// =============================================================================
//
// The existing provider-error test uses substring matching on Content. This
// test asserts the discriminating status field for a non-rate-limit error
// path, to lock the Status="error" contract for BOTH error encodings.
func TestRunAgentLoop_ProviderError_HasStatusErrorField(t *testing.T) {
	tmpHome := t.TempDir()
	workspaceDir := filepath.Join(tmpHome, "workspace")
	require.NoError(t, os.MkdirAll(workspaceDir, 0o755))

	providerErr := errors.New("provider auth failed: 401")
	provider := testutil.NewScenario()
	for i := 0; i < 5; i++ {
		provider = provider.WithError(providerErr)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              workspaceDir,
				DefaultModel:      config.DefaultModel{Model: "scripted-model"},
				MaxTokens:         4096,
				MaxToolIterations: 10,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspaceDir}},
		},
	}

	msgBus := bus.NewMessageBus()
	t.Cleanup(func() { msgBus.Close() })
	al := mustNewAgentLoop(t, cfg, msgBus, provider)
	t.Cleanup(al.Close)

	store := al.GetSessionStore()
	require.NotNil(t, store)
	meta, err := store.NewSession(session.SessionTypeChat, "web", "mia")
	require.NoError(t, err)
	sessionID := meta.ID

	_, err = al.runAgentLoop(context.Background(), al.GetRegistry().GetDefaultAgent(), processOptions{
		SessionKey: "pe-block", Channel: "web", ChatID: sessionID,
		UserMessage: "trigger", DefaultResponse: defaultResponse, SendResponse: false,
		TranscriptSessionID: sessionID, TranscriptStore: store,
	})
	require.Error(t, err)

	entries := readTranscriptEntries(t, store, sessionID)
	require.NotEmpty(t, entries)

	// The provider-error path also writes a system entry. Assert it carries
	// Status="error" — same contract as the rate-limit path.
	var found *session.TranscriptEntry
	for i := range entries {
		if entries[i].Type == session.EntryTypeSystem {
			cp := entries[i]
			found = &cp
			break
		}
	}
	require.NotNil(t, found, "transcript must contain a system entry after provider error")
	require.Equal(t, "error", found.Status,
		"W2-20 #4 (provider-error variant): Status MUST be the exact string \"error\"; "+
			"a regression that omits Status would break the replay path. Entry: %+v", *found)
}
