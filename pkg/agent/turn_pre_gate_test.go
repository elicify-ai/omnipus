// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// ADR-068 T068-12 — the pre-turn `model_unassigned` refusal and the ORDER of
// the three pre-turn gates.
//
// Spec: docs/internal/specs/adr-068-providers-ux-spec.md FR-014 (the derived
// needs_model predicate), FR-015 (typed refusal, zero provider calls),
// SC-013, and the WS/LLMError precedence constraint (MAJ-008, X-02, X-09).
// Scenario "Turn to an agent without a model is refused, not re-pointed"
// (US-3.AC4). TDD rows 19 and 23a.
//
// The gate order under test is:
//
//	needs_provider (ADR-067)  →  model_unassigned (here)  →  context_window_unknown (ADR-066)
//
// Each stage's fixture below satisfies its OWN predicate and at least one
// LATER one, so the code a turn actually ends with proves which gate ran
// first. If the ordering ever inverts, these tests fail naming the later
// code — the failure identifies the regression rather than merely reporting
// one.

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
	"github.com/elicify-ai/omnipus/pkg/logger"
)

// preGateFixture is one fully-booted loop with a healthy workspace, ready to
// drive a single scheduled turn at a named agent.
type preGateFixture struct {
	loop     *AgentLoop
	provider *testutil.ScenarioProvider
}

// newPreGateFixture boots a loop against cfg. The workspace its agents belong
// to is created by preGateHome, so the EARLIER workspace gate always passes
// and the pre-turn gate under test is what refuses.
//
// The caller owns cfg entirely: these tests are about which gate fires for a
// given configuration, so nothing here fills in a default model, a provider
// row, or an agent home behind the caller's back.
func newPreGateFixture(t *testing.T, cfg *config.Config) *preGateFixture {
	t.Helper()

	provider := testutil.NewScenario().WithText("should-never-be-called")
	msgBus := bus.NewMessageBus()
	al, err := NewAgentLoop(cfg, msgBus, provider)
	require.NoError(t, err,
		"a missing model is a per-agent turn refusal, never a boot failure")
	t.Cleanup(func() { al.Close() })

	return &preGateFixture{loop: al, provider: provider}
}

// preGateHome creates OMNIPUS_HOME with a workspace whose core team is
// agentIDs, plus a home directory per agent, and returns the home path.
func preGateHome(t *testing.T, agentIDs ...string) string {
	t.Helper()
	tmpHome := t.TempDir()
	t.Setenv("OMNIPUS_HOME", tmpHome)

	workspacesDir := filepath.Join(tmpHome, "workspaces")
	require.NoError(t, os.MkdirAll(filepath.Join(workspacesDir, "ws-1", "work"), 0o755))
	quoted := make([]string, 0, len(agentIDs))
	for _, id := range agentIDs {
		quoted = append(quoted, `"`+id+`"`)
		require.NoError(t, os.MkdirAll(filepath.Join(tmpHome, "agents", id), 0o755))
	}
	wsJSON := `{"id":"ws-1","core_team":[` + strings.Join(quoted, ",") + `]}`
	require.NoError(t, os.WriteFile(
		filepath.Join(workspacesDir, "ws-1.json"), []byte(wsJSON), 0o644))
	return tmpHome
}

// runPreGateTurn drives one scheduled turn at agentID and returns the turn
// error plus every ErrorPayload the loop emitted. ProcessScheduled pins the
// acting agent directly (no routing), which is what lets one loop drive
// several differently-degraded agents in the same config.
func (f *preGateFixture) runPreGateTurn(t *testing.T, agentID string) (error, []ErrorPayload, []Event) {
	t.Helper()

	sub := f.loop.SubscribeEvents(32)
	defer f.loop.UnsubscribeEvents(sub.ID)

	meta, err := f.loop.GetSessionStore().NewScheduledSession(agentID)
	require.NoError(t, err)
	_, procErr := f.loop.ProcessScheduled(
		context.Background(), agentID, meta.ID, "hello", "scheduled", meta.ID)

	events := drainEvents(sub.C)
	var payloads []ErrorPayload
	for _, e := range events {
		if e.Kind != EventKindError {
			continue
		}
		if p, ok := e.Payload.(ErrorPayload); ok {
			payloads = append(payloads, p)
		}
	}
	return procErr, payloads, events
}

// errorCodes returns the codes carried by every emitted ErrorPayload, for
// assertion messages that name what actually happened.
func errorCodes(payloads []ErrorPayload) []string {
	out := make([]string, 0, len(payloads))
	for _, p := range payloads {
		out = append(out, p.Code)
	}
	return out
}

// assertRefusedWith asserts the turn ended as a REAL failed turn carrying
// exactly `want` — turn.start emitted, a typed EventKindError with the
// catalogue copy, turn.end status error, and no other pre-turn refusal code
// anywhere in the stream.
func assertRefusedWith(
	t *testing.T,
	want LLMErrorCode,
	payloads []ErrorPayload,
	events []Event,
) {
	t.Helper()

	var sawTurnStart, sawTurnEndError bool
	for _, e := range events {
		switch e.Kind {
		case EventKindTurnStart:
			sawTurnStart = true
		case EventKindTurnEnd:
			if p, ok := e.Payload.(TurnEndPayload); ok && p.Status == TurnEndStatusError {
				sawTurnEndError = true
			}
		}
	}
	require.True(t, sawTurnStart,
		"the refusal must be a registered turn; events=%v", eventKinds(events))
	require.NotEmpty(t, payloads,
		"the refusal must emit EventKindError; events=%v", eventKinds(events))

	otherGateCodes := []LLMErrorCode{
		CodeNeedsProvider, CodeModelUnassigned, CodeContextWindowUnknown,
	}
	found := false
	for _, p := range payloads {
		if p.Code == string(want) {
			found = true
			assert.Equal(t, UserMessageForCode(want), p.Message,
				"the live error must carry the catalogue copy, not the raw sentinel")
			continue
		}
		for _, other := range otherGateCodes {
			assert.NotEqual(t, string(other), p.Code,
				"gate order: a %s turn must never reach the %s gate", want, other)
		}
	}
	assert.True(t, found,
		"EventKindError must carry code %s; got codes=%v", want, errorCodes(payloads))
	assert.True(t, sawTurnEndError, "the turn must end as TurnEndStatusError")
}

// unassignedModelConfig builds a config in which `agent-a` has NO model at
// all: it pins no primary, and agents.defaults.default_model names none
// either. A provider IS configured — this is the "connected provider, no
// model chosen" state, so the refusal can only be about the missing model.
func unassignedModelConfig(t *testing.T, tmpHome string) *config.Config {
	t.Helper()
	t.Setenv("T068_12_OPENAI_KEY", "sk-openai")
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                tmpHome,
				MaxTokens:           4096,
				MaxToolIterations:   10,
				RestrictToWorkspace: true,
				// DefaultModel deliberately zero: FR-013's provider deletion
				// leaves exactly this state behind.
			},
			List: []config.AgentConfig{
				{ID: "agent-a", Home: filepath.Join(tmpHome, "agents", "agent-a")},
			},
		},
		Providers: []*config.ModelConfig{{
			Name:      "openai-1",
			Provider:  "openai",
			Model:     "gpt-5.2",
			APIBase:   "https://api.openai.com/v1",
			APIKeyRef: "T068_12_OPENAI_KEY",
		}},
	}
	cfg.Context = config.DefaultContextSettings()
	return cfg
}

// TestTurn_ModelUnassignedTypedError — TDD row 19 (FR-015, SC-013, US-3.AC4).
// A turn sent to an agent with no model is refused with LLMError code
// model_unassigned, makes ZERO upstream requests, and leaves the agent's
// stored model exactly as it was: empty. A refusal never re-points an agent
// at some other model.
func TestTurn_ModelUnassignedTypedError(t *testing.T) {
	installWindowTestCatalog(t, 1_048_576)
	installLiveWindowStub(t, nil)

	tmpHome := preGateHome(t, "agent-a")
	cfg := unassignedModelConfig(t, tmpHome)

	readLog := captureLogFile(t, logger.WARN)
	f := newPreGateFixture(t, cfg)

	agentA, ok := f.loop.GetRegistry().GetAgent("agent-a")
	require.True(t, ok, "the agent must still be registered — a missing model is not a boot failure")
	require.Empty(t, agentA.Model, "fixture precondition: the agent has no model")
	needsProvider, _ := agentA.needsProviderSnapshot()
	require.False(t, needsProvider,
		"fixture precondition: the earlier provider gate must NOT apply, or this test passes for the wrong reason")
	require.True(t, agentA.needsModelSnapshot(), "the instance must carry the needs_model state")

	procErr, payloads, events := f.runPreGateTurn(t, "agent-a")

	require.Error(t, procErr, "a turn on an agent with no model must be refused")
	assert.True(t, errors.Is(procErr, ErrAgentModelUnassigned),
		"refusal must wrap ErrAgentModelUnassigned, got: %v", procErr)
	assertRefusedWith(t, CodeModelUnassigned, payloads, events)

	assert.Zero(t, f.provider.CallCount(),
		"SC-013: zero upstream requests for a model_unassigned refusal")

	// US-3.AC4: the agent is refused, not re-pointed. Neither the live
	// instance nor the persisted config acquired a model.
	assert.Empty(t, agentA.Model, "the refusal must not assign the agent a model")
	liveCfg := f.loop.GetConfig()
	require.Len(t, liveCfg.Agents.List, 1)
	if m := liveCfg.Agents.List[0].Model; m != nil {
		assert.Empty(t, strings.TrimSpace(m.Primary),
			"the refusal must not write a model into the agent's config")
	}
	assert.True(t, liveCfg.Agents.Defaults.DefaultModel.IsZero(),
		"the refusal must not invent a global default model either")

	log := readLog()
	assert.Contains(t, log, "Turn refused: the agent has no model assigned",
		"the refusal is logged at WARN; log=%s", log)
}

// TestTurn_PreTurnGateOrder — TDD row 23a (cross-spec Q6; MAJ-008, X-02,
// X-09). The three pre-turn gates are evaluated in a fixed order, and each
// subtest's agent satisfies its own gate AND a later one, so the code the
// turn ends with is proof of the ordering rather than of the predicate alone.
func TestTurn_PreTurnGateOrder(t *testing.T) {
	t.Run("unknown provider and no usable model yields needs_provider", func(t *testing.T) {
		installWindowTestCatalog(t, 1_048_576)
		installLiveWindowStub(t, nil)

		tmpHome := preGateHome(t, "agent-b")
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home:                tmpHome,
					MaxTokens:           4096,
					MaxToolIterations:   10,
					RestrictToWorkspace: true,
				},
				List: []config.AgentConfig{{
					ID:   "agent-b",
					Home: filepath.Join(tmpHome, "agents", "agent-b"),
					// Half a custom row's worth of pinning: the id "nope" is
					// neither a catalog id nor a constructible custom row, so
					// the agent is needs_provider — and therefore ALSO
					// needs_model under FR-014's second half, plus the
					// loopback row below leaves its window unknown. All three
					// predicates hold; only the first may win.
					Model: &config.AgentModelConfig{Primary: "nope/x", Provider: "nope"},
				}},
			},
			Providers: []*config.ModelConfig{{
				Name:     "nope",
				Provider: "nope",
				Model:    "nope/x",
				APIBase:  "http://127.0.0.1:9/v1",
			}},
		}
		cfg.Context = config.DefaultContextSettings()

		f := newPreGateFixture(t, cfg)
		agentB, ok := f.loop.GetRegistry().GetAgent("agent-b")
		require.True(t, ok)
		needsProvider, id := agentB.needsProviderSnapshot()
		require.True(t, needsProvider, "fixture precondition: stage 1 applies")
		assert.Equal(t, "nope", id, "the degrade names the operator's own spelling")
		require.True(t, agentB.needsModelSnapshot(),
			"fixture precondition: stage 2 ALSO applies — SC-013's 'both apply' case")

		procErr, payloads, events := f.runPreGateTurn(t, "agent-b")

		require.Error(t, procErr)
		assert.True(t, errors.Is(procErr, ErrAgentNeedsProvider),
			"stage 1 wins when both apply (SC-013), got: %v", procErr)
		assert.False(t, errors.Is(procErr, ErrAgentModelUnassigned),
			"a needs_provider agent must never end with model_unassigned")
		assertRefusedWith(t, CodeNeedsProvider, payloads, events)
		assert.Zero(t, f.provider.CallCount())
	})

	t.Run("configured provider and empty model yields model_unassigned", func(t *testing.T) {
		installWindowTestCatalog(t, 1_048_576)
		installLiveWindowStub(t, nil)

		tmpHome := preGateHome(t, "agent-a")
		cfg := unassignedModelConfig(t, tmpHome)

		f := newPreGateFixture(t, cfg)
		agentA, ok := f.loop.GetRegistry().GetAgent("agent-a")
		require.True(t, ok)
		needsProvider, _ := agentA.needsProviderSnapshot()
		require.False(t, needsProvider, "fixture precondition: stage 1 does NOT apply")
		require.True(t, agentA.needsModelSnapshot(), "fixture precondition: stage 2 applies")

		procErr, payloads, events := f.runPreGateTurn(t, "agent-a")

		require.Error(t, procErr)
		assert.True(t, errors.Is(procErr, ErrAgentModelUnassigned),
			"stage 2 is what refuses once stage 1 passes, got: %v", procErr)
		assert.False(t, errors.Is(procErr, ErrAgentNeedsProvider),
			"a configured provider must not be reported as missing")
		assertRefusedWith(t, CodeModelUnassigned, payloads, events)
		assert.Zero(t, f.provider.CallCount())
	})

	t.Run("assigned model on an unsized local endpoint yields context_window_unknown", func(t *testing.T) {
		installWindowTestCatalog(t, 1_048_576)
		installLiveWindowStub(t, nil)

		tmpHome := preGateHome(t, "agent-c")
		cfg := &config.Config{
			Agents: config.AgentsConfig{
				Defaults: config.AgentDefaults{
					Home:                tmpHome,
					DefaultModel:        config.DefaultModel{Provider: "my-proxy", Model: "local-model"},
					MaxTokens:           4096,
					MaxToolIterations:   10,
					RestrictToWorkspace: true,
				},
				List: []config.AgentConfig{{
					ID:   "agent-c",
					Home: filepath.Join(tmpHome, "agents", "agent-c"),
				}},
			},
			// A complete custom row (api_base AND protocol) at a loopback
			// host: the provider is KNOWN, the model IS assigned, so stages 1
			// and 2 both pass — and its window is unknown, so stage 3 is what
			// refuses. This is the assertion that stage 2 does not swallow
			// turns it has no business refusing.
			Providers: []*config.ModelConfig{{
				Name:     "local-model",
				Model:    "local-model",
				Provider: "my-proxy",
				APIBase:  "http://127.0.0.1:8000/v1",
				Protocol: "openai-compatible",
			}},
		}
		cfg.Context = config.DefaultContextSettings()

		f := newPreGateFixture(t, cfg)
		agentC, ok := f.loop.GetRegistry().GetAgent("agent-c")
		require.True(t, ok)
		needsProvider, _ := agentC.needsProviderSnapshot()
		require.False(t, needsProvider, "fixture precondition: stage 1 does NOT apply")
		require.False(t, agentC.needsModelSnapshot(), "fixture precondition: stage 2 does NOT apply")
		require.True(t, agentC.WindowUnknown, "fixture precondition: stage 3 applies")

		procErr, payloads, events := f.runPreGateTurn(t, "agent-c")

		require.Error(t, procErr)
		assert.True(t, errors.Is(procErr, ErrContextWindowUnknown),
			"stage 3 is what refuses once stages 1 and 2 pass, got: %v", procErr)
		assert.False(t, errors.Is(procErr, ErrAgentModelUnassigned),
			"an agent WITH a model must never be refused as model_unassigned")
		assertRefusedWith(t, CodeContextWindowUnknown, payloads, events)
		assert.Zero(t, f.provider.CallCount())
	})
}
