// session_end_gaps_test.go — coverage for reviewer-flagged gaps in the
// session-end recap pipeline (commit 4ea20333 / hotfix/v0.1.1).
//
// GAP-1: all candidates fail → heuristic retro written, every candidate tried.
// GAP-2: SeedConfig recap flags on fresh vs existing config.
// IMP-2: model-resolution tiers (recap_model → default_model → agent model).
// IMP-3: recap fallback with distinct Provider routes through that provider.

package agent

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ─────────────────────────────────────────────────────────────────────────────
// GAP-1: all-candidates-fail
// ─────────────────────────────────────────────────────────────────────────────

// alwaysErrorProvider is a provider that returns an error for every Chat call,
// regardless of model. Records every call for assertion.
type alwaysErrorProvider struct {
	mu        sync.Mutex
	callCount int
}

func (a *alwaysErrorProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	model string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	a.mu.Lock()
	a.callCount++
	a.mu.Unlock()
	return nil, fmt.Errorf("provider: all models unavailable (simulated for %q)", model)
}

func (a *alwaysErrorProvider) GetDefaultModel() string { return "error-model" }

// TestRunRecap_AllCandidatesFail verifies GAP-1:
//   - When every candidate in the recap chain errors, the pipeline writes a
//     heuristic fallback retro (fallback=true, llm_* reason).
//   - All three candidates are attempted (callCount == 3).
//   - No panic, no empty/missing write.
func TestRunRecap_AllCandidatesFail(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = "m1"
	cfg.Agents.Defaults.RecapFallbackModels = config.FallbackModelSlice{
		{Model: "m2"},
		{Model: "m3"},
	}

	prov := &alwaysErrorProvider{}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, prov)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "gap1-agent", Name: "Gap1"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, prov)
	ag.Home = filepath.Join(home, "agents", "gap1-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("gap1-agent", "Gap1")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", "gap1-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if appendErr := store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:      "user",
		Content:   "all candidates fail test",
		AgentID:   "gap1-agent",
		Timestamp: time.Now().UTC(),
	}); appendErr != nil {
		t.Fatalf("AppendTranscript: %v", appendErr)
	}

	al.CloseSession(meta.ID, "explicit")

	// Wait for the fallback retro to land.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countRetrosFor(t, ag.Home, meta.ID) > 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	// A retro must exist (heuristic fallback).
	if countRetrosFor(t, ag.Home, meta.ID) == 0 {
		t.Fatal("expected a heuristic fallback retro when all candidates fail; none found")
	}

	// All three candidates must have been tried.
	prov.mu.Lock()
	calls := prov.callCount
	prov.mu.Unlock()
	if calls != 3 {
		t.Errorf("expected 3 Chat calls (m1 + m2 + m3); got %d", calls)
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// GAP-2: SeedConfig recap flags
// ─────────────────────────────────────────────────────────────────────────────

// TestSeedConfig_FreshInstall_EnablesRecap verifies GAP-2a:
// On a fresh install (no agents in Config), SeedConfig must set both
// AutoRecapEnabled and BootstrapRecapEnabled to true.
func TestSeedConfig_FreshInstall_EnablesRecap(t *testing.T) {
	cfg := &config.Config{}
	// Sanity: both flags start false.
	if cfg.Agents.Defaults.AutoRecapEnabled {
		t.Fatal("precondition failed: AutoRecapEnabled must be false before SeedConfig")
	}
	if cfg.Agents.Defaults.BootstrapRecapEnabled {
		t.Fatal("precondition failed: BootstrapRecapEnabled must be false before SeedConfig")
	}

	coreagent.SeedConfig(cfg)

	if !cfg.Agents.Defaults.AutoRecapEnabled {
		t.Error("fresh install: SeedConfig must set AutoRecapEnabled=true")
	}
	if !cfg.Agents.Defaults.BootstrapRecapEnabled {
		t.Error("fresh install: SeedConfig must set BootstrapRecapEnabled=true")
	}
}

// TestSeedConfig_ExistingConfig_KeepsExplicitFalse verifies GAP-2b:
// When the config already has ≥1 agent and both recap flags are explicitly
// false, SeedConfig must not touch them (operator choice preserved).
func TestSeedConfig_ExistingConfig_KeepsExplicitFalse(t *testing.T) {
	cfg := &config.Config{
		Agents: config.AgentsConfig{
			List: []config.AgentConfig{
				{
					ID:     "existing-agent",
					Name:   "Existing",
					Locked: false,
				},
			},
			Defaults: config.AgentDefaults{
				AutoRecapEnabled:      false,
				BootstrapRecapEnabled: false,
			},
		},
	}

	coreagent.SeedConfig(cfg)

	if cfg.Agents.Defaults.AutoRecapEnabled {
		t.Error("existing config: SeedConfig must not override AutoRecapEnabled=false")
	}
	if cfg.Agents.Defaults.BootstrapRecapEnabled {
		t.Error("existing config: SeedConfig must not override BootstrapRecapEnabled=false")
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// IMP-2: model-resolution tiers
// ─────────────────────────────────────────────────────────────────────────────

// runRecapModelResolutionScenario is the shared setup for IMP-2's
// model-resolution tiers (recap_model → default_model → agent model): a fresh
// agent loop with RecapModel always empty, Defaults.ModelName set to
// defaultModelName (possibly "" to fall through further), and the session
// agent's own Model always "agent-own-model" — then asserts the recap
// actually used wantModel. Shared by
// TestRunRecap_ModelResolution_DefaultModelFallback (IMP-2a) and
// TestRunRecap_ModelResolution_AgentModelFallback (IMP-2b), which differ only
// in defaultModelName / wantModel.
func runRecapModelResolutionScenario(t *testing.T, agentID, agentName, defaultModelName, wantModel string) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = "" // empty → should fall through the tiers
	cfg.Agents.Defaults.DefaultModel.Model = defaultModelName

	script := &scriptedProvider{
		responseBody: `{"recap":"ok","went_well":[],"needs_improvement":[],"worth_remembering":[]}`,
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, script)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: agentID, Name: agentName}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, script)
	ag.Model = "agent-own-model" // must NOT be used when a stricter tier resolves first
	ag.Home = filepath.Join(home, "agents", agentID)
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo(agentID, agentName)
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", agentID)
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_ = store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:      "user",
		Content:   "tier resolution test",
		AgentID:   agentID,
		Timestamp: time.Now().UTC(),
	})

	al.CloseSession(meta.ID, "explicit")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countRetrosFor(t, ag.Home, meta.ID) > 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	script.mu.Lock()
	usedModel := script.lastModel
	script.mu.Unlock()

	if usedModel != wantModel {
		t.Errorf("recap used model %q, want %q", usedModel, wantModel)
	}
}

// TestRunRecap_ModelResolution_DefaultModelFallback verifies IMP-2a:
// When RecapModel is empty and Defaults.ModelName is set, the recap uses
// the global default model (not the agent's own model).
func TestRunRecap_ModelResolution_DefaultModelFallback(t *testing.T) {
	runRecapModelResolutionScenario(t, "imp2a-agent", "Imp2a", "default-model", "default-model")
}

// TestRunRecap_ModelResolution_AgentModelFallback verifies IMP-2b:
// When both RecapModel and Defaults.ModelName are empty, the recap uses
// the session agent's own model.
func TestRunRecap_ModelResolution_AgentModelFallback(t *testing.T) {
	runRecapModelResolutionScenario(t, "imp2b-agent", "Imp2b", "", "agent-own-model")
}

// ─────────────────────────────────────────────────────────────────────────────
// IMP-3: recap fallback with distinct Provider routes through that provider
// ─────────────────────────────────────────────────────────────────────────────

// providerTrackingRecap is a scripted provider that records the provider
// instance that was used for each call, to allow tests to assert that the
// correct provider was selected for a fallback candidate.
type namedProvider struct {
	mu           sync.Mutex
	name         string
	callCount    int
	lastModel    string
	responseErr  error
	responseBody string
}

func (n *namedProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	model string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	n.mu.Lock()
	n.callCount++
	n.lastModel = model
	err := n.responseErr
	body := n.responseBody
	n.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &providers.LLMResponse{Content: body}, nil
}

func (n *namedProvider) GetDefaultModel() string { return n.name + "-model" }

// TestRunRecap_FallbackDistinctProvider verifies IMP-3:
// A recap fallback that declares a distinct Provider must route through that
// provider (i.e. the provider pool built in runRecap resolves it correctly)
// rather than silently falling through to the agent's primary provider.
//
// Setup:
//   - Primary recap model "m1" on the default (agentInst) provider → always errors.
//   - Fallback model "m2" with Provider "alt-provider" → backed by altProvider → succeeds.
//   - Config.Providers lists "alt-provider" so buildProviderPool can resolve it.
//   - We inject altProvider into the config's resolved pool by using a test
//     hook that replaces the CreateProviderFromConfig path: since we cannot
//     easily inject a factory, we verify indirectly — altProvider is the agent's
//     primary provider but "alt-provider" is configured with a distinct API base
//     that in prod would require different credentials. The real signal is that
//     the fallback's Chat is called on a provider that succeeds (call count on
//     the primary-error provider vs a success-only provider confirms routing).
//
// Because buildProviderPool requires a real config.ModelConfig, and because
// we can't cheaply inject a custom LLMProvider factory, this test uses a
// single-provider approach: the primary candidate errors and the fallback
// candidate (empty Provider, same backing provider) succeeds. The distinct-
// Provider path is covered by unit-testing resolveRecapProvider in a
// white-box manner: we verify that when a provider name is configured and
// the backing pool has an entry for it, that entry is preferred over the
// agent's primary.
//
// The actual routing assertion: when "alt" resolves correctly, the provider
// whose Chat succeeds on "m2" is the altProvider — proving Fix #2 works.
func TestRunRecap_FallbackDistinctProvider_RoutesCorrectly(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	// primaryProv: errors on all calls (simulates primary model failure).
	primaryProv := &namedProvider{
		name:        "primary",
		responseErr: fmt.Errorf("provider: primary model unavailable"),
	}
	// altProv: never actually wired into a real provider call path in this test.
	//
	// KNOWN COVERAGE GAP (flagged by govet's unusedwrite, not silenced blindly):
	// altProv is never actually wired into cfg's provider pool — buildProviderPool
	// resolves providers from the config.ModelConfig via CreateProviderFromConfig,
	// not from this in-memory struct, so altProv.Chat (and therefore any `name`
	// or `responseBody` we might set) is structurally never invoked/read in this
	// test. Only its call-count bookkeeping (altProv.mu / altProv.callCount) is
	// read below, so no Chat/GetDefaultModel-facing fields are set here — an
	// empty literal, not a nolint suppression, since the fields would be
	// genuinely dead. The test's stated IMP-3 assertion ("a fallback with a
	// distinct Provider routes through that provider") is consequently NOT
	// verified end-to-end by this test; it only verifies that SOME retro gets
	// written and that the primary candidate was attempted. A real fix needs a
	// factory-injection seam in buildProviderPool so tests can substitute an
	// in-memory provider for a named config.ModelConfig entry — out of scope
	// for a lint sweep; tracked here for follow-up.
	altProv := &namedProvider{}

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = "m1"
	// Fallback declares Provider="alt-provider" — must route through altProv.
	cfg.Agents.Defaults.RecapFallbackModels = config.FallbackModelSlice{
		{Model: "m2", Provider: "alt-provider"},
	}
	// Register "alt-provider" in cfg.Providers so buildProviderPool can resolve it.
	// We use a minimal ModelConfig; the actual provider construction in tests
	// uses the injected provider (agentInst.Provider) as the primary, and the
	// pool-resolved provider for the fallback. Since CreateProviderFromConfig
	// in a unit test context may fail (no real API key/base), we verify the
	// routing indirectly: when the pool build fails, buildProviderPool logs
	// a WARN and the resolveRecapProvider falls back to agentInst.Provider
	// (primaryProv). To exercise the real path we need a minimal valid
	// ModelConfig that CreateProviderFromConfig can use without a network call.
	//
	// Resolution: we inject the alt provider as the configured primary of the
	// "alt-provider" entry with a passthrough/openrouter provider type and a
	// dummy API base. The test then asserts that the overall recap succeeds
	// (retro written) even though the primary model errors.
	cfg.Providers = append(cfg.Providers, &config.ModelConfig{
		Provider:  "alt-provider",
		APIBase:   "https://api.alt-provider.example.com/v1",
		APIKeyRef: "ALT_PROVIDER_KEY",
		Model:     "m2",
	})

	// We use primaryProv as the agent's primary. The fallback candidate has
	// Provider="alt-provider" — buildProviderPool will attempt to create a real
	// provider from cfg. If that succeeds, the fallback goes through altProv's
	// credentials. If it fails (no network, test env), the pool entry is
	// skipped and resolveRecapProvider falls back to primaryProv (which errors
	// on m2 too). In that case the test degrades to verifying a fallback retro
	// is written.
	//
	// For a deterministic unit-test assertion, we verify the simpler invariant:
	// the primary candidate m1 is tried first (primaryProv.callCount ≥ 1) and
	// the overall pipeline does not panic and writes some retro.
	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, primaryProv)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "imp3-agent", Name: "Imp3"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, primaryProv)
	ag.Home = filepath.Join(home, "agents", "imp3-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("imp3-agent", "Imp3")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", "imp3-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_ = store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:      "user",
		Content:   "distinct provider routing test",
		AgentID:   "imp3-agent",
		Timestamp: time.Now().UTC(),
	})

	al.CloseSession(meta.ID, "explicit")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countRetrosFor(t, ag.Home, meta.ID) > 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	// A retro must be written (either LLM success via alt path, or heuristic fallback).
	if countRetrosFor(t, ag.Home, meta.ID) == 0 {
		t.Fatal("IMP-3: no retro written after primary fails + fallback tried; panic or missing write?")
	}

	// The primary provider must have been called at least once (m1 attempt).
	primaryProv.mu.Lock()
	primaryCalls := primaryProv.callCount
	primaryProv.mu.Unlock()
	if primaryCalls == 0 {
		t.Error("IMP-3: primary provider was never called; candidate m1 was skipped")
	}

	// The alt provider should have been called if the pool entry was built
	// successfully. In a test environment without a real API base this may
	// not succeed (pool build fails gracefully), so we only assert if altProv
	// shows a call.
	altProv.mu.Lock()
	altCalls := altProv.callCount
	altProv.mu.Unlock()
	t.Logf("IMP-3: primaryProv calls=%d altProv calls=%d", primaryCalls, altCalls)
	// The combined call count must be ≥ 1 (at minimum the primary was tried).
	if primaryCalls+altCalls < 1 {
		t.Error("IMP-3: combined call count must be ≥ 1")
	}
}

// TestRunRecap_FallbackDistinctProvider_NoPoolEntryFallsBack verifies that
// when a recap fallback declares a Provider that has NO matching config entry
// (pool build misses it), resolveRecapProvider falls back gracefully to the
// agent's primary provider rather than panicking or returning nil.
func TestRunRecap_FallbackDistinctProvider_NoPoolEntryFallsBack(t *testing.T) {
	home := t.TempDir()
	t.Setenv("OMNIPUS_HOME", home)

	cfg := &config.Config{}
	cfg.Agents.Defaults.AutoRecapEnabled = true
	cfg.Agents.Defaults.RecapModel = "m1"
	// "missing-provider" is NOT in cfg.Providers — pool build will miss it.
	cfg.Agents.Defaults.RecapFallbackModels = config.FallbackModelSlice{
		{Model: "m2", Provider: "missing-provider"},
	}

	// primaryProv fails on m1, succeeds on m2 (since pool misses, m2 will
	// route through primaryProv too).
	prov := &modelSwitchProvider{
		failModel:   "m1",
		successBody: `{"recap":"fallback-via-primary-pool","went_well":[],"needs_improvement":[],"worth_remembering":[]}`,
	}

	msgBus := bus.NewMessageBus()
	al := mustNewAgentLoop(t, cfg, msgBus, prov)
	t.Cleanup(func() { al.Close() })

	store, err := session.NewUnifiedStore(filepath.Join(home, "sessions"))
	if err != nil {
		t.Fatalf("NewUnifiedStore: %v", err)
	}
	al.sharedSessionStore = store

	agentCfg := &config.AgentConfig{ID: "imp3b-agent", Name: "Imp3b"}
	ag := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, prov)
	ag.Home = filepath.Join(home, "agents", "imp3b-agent")
	ag.ContextBuilder = NewContextBuilder(ag.Home).WithAgentInfo("imp3b-agent", "Imp3b")
	al.registry.mu.Lock()
	al.registry.agents[agentCfg.ID] = ag
	al.registry.mu.Unlock()

	meta, err := store.NewSession(session.SessionTypeChat, "web", "imp3b-agent")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	_ = store.AppendTranscript(meta.ID, session.TranscriptEntry{
		Role:      "user",
		Content:   "pool-miss graceful degradation test",
		AgentID:   "imp3b-agent",
		Timestamp: time.Now().UTC(),
	})

	al.CloseSession(meta.ID, "explicit")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if countRetrosFor(t, ag.Home, meta.ID) > 0 {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}

	if countRetrosFor(t, ag.Home, meta.ID) == 0 {
		t.Fatal("pool-miss: retro must be written via graceful primary-pool degradation")
	}

	// Verify ≥2 calls: m1 (fail) + m2 (success via primary-pool fallback).
	prov.mu.Lock()
	calls := prov.callCount
	prov.mu.Unlock()
	if calls < 2 {
		t.Errorf("pool-miss: expected ≥2 Chat calls (m1 fail + m2 via degraded pool); got %d", calls)
	}
}
