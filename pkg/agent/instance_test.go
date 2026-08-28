package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/providers"
)

func TestNewAgentInstance_UsesDefaultsTemperatureAndMaxTokens(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         1234,
				MaxToolIterations: 5,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	configuredTemp := 1.0
	cfg.Agents.Defaults.Temperature = &configuredTemp

	provider := &mockProvider{}
	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, provider)

	if agent.MaxTokens != 1234 {
		t.Fatalf("MaxTokens = %d, want %d", agent.MaxTokens, 1234)
	}
	if agent.Temperature != 1.0 {
		t.Fatalf("Temperature = %f, want %f", agent.Temperature, 1.0)
	}
}

func TestNewAgentInstance_DefaultsTemperatureWhenZero(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         1234,
				MaxToolIterations: 5,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	configuredTemp := 0.0
	cfg.Agents.Defaults.Temperature = &configuredTemp

	provider := &mockProvider{}
	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, provider)

	if agent.Temperature != 0.0 {
		t.Fatalf("Temperature = %f, want %f", agent.Temperature, 0.0)
	}
}

func TestNewAgentInstance_DefaultsTemperatureWhenUnset(t *testing.T) {
	tmpDirOuter, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              tmpDir,
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         1234,
				MaxToolIterations: 5,
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
	}

	provider := &mockProvider{}
	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, provider)

	if agent.Temperature != 0.7 {
		t.Fatalf("Temperature = %f, want %f", agent.Temperature, 0.7)
	}
}

func TestNewAgentInstance_FallbackModelsPerEntryProvider(t *testing.T) {
	// FR-007: a fallback declared with [{model, provider}] must resolve to a
	// candidate that carries the explicit Provider — distinct from the
	// primary's provider — so a rate-limit on the primary routes through the
	// fallback's own credentials at LLM-call time.
	tmpDirOuter, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "openai/gpt-5"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		Providers: []*config.ModelConfig{
			{
				Name:     "gpt-5",
				Model:    "openai/gpt-5",
				Provider: "openrouter",
				APIBase:  "https://openrouter.ai/api/v1",
			},
			{
				Name:     "claude-haiku",
				Model:    "anthropic/claude-haiku-4-5",
				Provider: "anthropic",
				APIBase:  "https://api.anthropic.com/v1",
			},
		},
	}

	agentCfg := &config.AgentConfig{
		ID: "mia",
		Model: &config.AgentModelConfig{
			Primary: "gpt-5",
		},
		FallbackModels: config.FallbackModelSlice{
			{Model: "claude-haiku", Provider: "anthropic"},
		},
	}

	provider := &mockProvider{}
	agent := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, provider)

	if len(agent.FallbackModels) != 1 {
		t.Fatalf("len(agent.FallbackModels) = %d, want 1", len(agent.FallbackModels))
	}
	if agent.FallbackModels[0].Provider != "anthropic" {
		t.Fatalf("agent.FallbackModels[0].Provider = %q, want %q", agent.FallbackModels[0].Provider, "anthropic")
	}
	if agent.FallbackModels[0].Model != "claude-haiku" {
		t.Fatalf("agent.FallbackModels[0].Model = %q, want %q", agent.FallbackModels[0].Model, "claude-haiku")
	}

	// Candidates must contain BOTH the primary and the explicit fallback.
	// The primary resolves through cfg.GetModelConfig (matches by Name
	// "gpt-5") and returns the entry's Model verbatim ("openai/gpt-5") —
	// so the primary candidate carries the explicit "openai/" prefix, and
	// ParseModelRef splits that into Provider=openai, Model=gpt-5.
	//
	// The KEY assertion is the SECOND candidate: the fallback MUST carry
	// its own Provider ("anthropic"), NOT the agent default provider
	// ("openrouter"). That is the FR-007 invariant the bug was violating.
	if len(agent.Candidates) != 2 {
		t.Fatalf("len(agent.Candidates) = %d, want 2", len(agent.Candidates))
	}
	if agent.Candidates[1].Provider != "anthropic" {
		t.Fatalf(
			"candidate[1].Provider = %q, want %q (FR-007: fallback must carry its own provider, not the agent's default %q)",
			agent.Candidates[1].Provider,
			"anthropic",
			cfg.Agents.Defaults.DefaultModel.Provider,
		)
	}
	// Because the fallback has a pinned Provider, the resolver is bypassed:
	// the Model is taken verbatim from the entry. The operator wrote the
	// model slug the way their provider expects it — we don't second-guess.
	if agent.Candidates[1].Model != "claude-haiku" {
		t.Fatalf("candidate[1].Model = %q, want %q (explicit Provider: model is verbatim)",
			agent.Candidates[1].Model, "claude-haiku")
	}

	// Sanity: the legacy Fallbacks []string field is empty — the modern
	// FallbackModels path was used.
	if len(agent.Fallbacks) != 0 {
		t.Fatalf("len(agent.Fallbacks) = %d, want 0 (legacy field unused)", len(agent.Fallbacks))
	}

	// GetProviderForCandidate MUST return a distinct provider instance for
	// the fallback (Provider="anthropic") — verifying the runtime half of
	// the FR-007 contract. The test config has no API keys, so the pool
	// build logs a WARN and skips that entry; the lookup MUST fall back to
	// the primary provider gracefully rather than panic / nil-deref.
	primary := agent.GetProviderForCandidate(providers.FallbackCandidate{Provider: "openai", Model: "gpt-5"})
	if primary == nil {
		t.Fatal("GetProviderForCandidate returned nil for primary candidate")
	}
	fb := agent.GetProviderForCandidate(providers.FallbackCandidate{Provider: "anthropic", Model: "claude-haiku-4-5"})
	if fb == nil {
		t.Fatal("GetProviderForCandidate returned nil for fallback candidate")
	}
	// Legacy wire shape (no Provider pinned): MUST return the primary's
	// provider, regardless of pool contents.
	legacy := agent.GetProviderForCandidate(providers.FallbackCandidate{Provider: "", Model: "anything"})
	if legacy != agent.Provider {
		t.Errorf("legacy candidate (no Provider) returned %p, want primary %p", legacy, agent.Provider)
	}
}

func TestNewAgentInstance_FallbackModelsPrefersExplicitOverLegacy(t *testing.T) {
	// When BOTH the modern FallbackModels and the legacy Model.Fallbacks are
	// populated, FallbackModels wins (FR-005 wire shape is the canonical
	// forward form). Legacy Fallbacks is retained on the instance for
	// backward compatibility with code that still reads it.
	tmpDirOuter, err := os.MkdirTemp("", "agent-instance-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDirOuter)
	// Nested one level below the freshly-made outer container so
	// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
	// session/task store at) is THIS test's own private tmpDirOuter,
	// never the shared OS temp root — see loop_test.go's
	// newTestAgentLoop doc comment for the leak this closes.
	tmpDir := filepath.Join(tmpDirOuter, "home")
	if err := os.MkdirAll(tmpDir, 0o700); err != nil {
		t.Fatalf("Failed to create nested home dir: %v", err)
	}

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpDir,
				DefaultModel: config.DefaultModel{Provider: "openrouter", Model: "openai/gpt-5"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
		},
		Providers: []*config.ModelConfig{
			{
				Name:     "gpt-5",
				Model:    "openai/gpt-5",
				Provider: "openrouter",
				APIBase:  "https://openrouter.ai/api/v1",
			},
			{
				Name:     "haiku-anthropic",
				Model:    "anthropic/claude-haiku-4-5",
				Provider: "anthropic",
				APIBase:  "https://api.anthropic.com/v1",
			},
			{
				Name:     "haiku-openrouter",
				Model:    "openrouter/anthropic/claude-haiku-4-5",
				Provider: "openrouter",
				APIBase:  "https://openrouter.ai/api/v1",
			},
		},
	}

	agentCfg := &config.AgentConfig{
		ID: "mia",
		Model: &config.AgentModelConfig{
			Primary:   "gpt-5",
			Fallbacks: []string{"haiku-openrouter"}, // legacy — should be ignored
		},
		FallbackModels: config.FallbackModelSlice{
			{Model: "haiku-anthropic", Provider: "anthropic"}, // modern — should win
		},
	}

	agent := NewAgentInstance(agentCfg, &cfg.Agents.Defaults, cfg, &mockProvider{})

	if len(agent.Candidates) != 2 {
		t.Fatalf("len(agent.Candidates) = %d, want 2", len(agent.Candidates))
	}
	if agent.Candidates[1].Provider != "anthropic" {
		t.Fatalf("candidate[1].Provider = %q, want %q (FallbackModels must override legacy)",
			agent.Candidates[1].Provider, "anthropic")
	}
	// When the modern FallbackModels entry has an explicit Provider, the
	// model slug is passed verbatim — operator intent is honored over alias
	// resolution.
	if agent.Candidates[1].Model != "haiku-anthropic" {
		t.Fatalf("candidate[1].Model = %q, want %q", agent.Candidates[1].Model, "haiku-anthropic")
	}
}

// TestAgentInstance_GetProviderForCandidate_PoolHonorsPinnedProvider locks in
// the runtime half of FR-007: when the ProviderPool has an entry for the
// candidate's pinned Provider, GetProviderForCandidate MUST return that
// entry — NOT the agent's primary provider. This is what makes a rate-limit
// on the primary's provider actually route through the fallback's own
// credentials at LLM-call time.
//
// The pool is pre-populated here (vs. via buildProviderPool) so the test
// doesn't depend on CreateProviderFromConfig succeeding — that path requires
// a real API key and would needlessly couple the test to live config. The
// contract under test is the pool lookup itself, which is the part that
// fires on every fallback attempt in production.
func TestAgentInstance_GetProviderForCandidate_PoolHonorsPinnedProvider(t *testing.T) {
	primary := &mockProvider{}
	anthropicProvider := &mockProvider{}

	agent := &AgentInstance{
		ID:       "mia",
		Provider: primary,
	}
	// Pool is published via StoreProviderPool (atomic.Pointer). Direct field
	// assignment would be a race hazard with GetProviderForCandidate, so the
	// exported API is the only supported publish path in production AND in
	// tests.
	agent.StoreProviderPool(map[string]providers.LLMProvider{
		"anthropic": anthropicProvider,
	})

	got := agent.GetProviderForCandidate(providers.FallbackCandidate{Provider: "anthropic", Model: "claude-haiku-4-5"})
	if got != anthropicProvider {
		t.Errorf("GetProviderForCandidate(anthropic) = %p, want %p (the pool entry, not the primary)",
			got, anthropicProvider)
	}

	// Empty pool entry for "openai" (the candidate's Provider is openai).
	// MUST fall back to primary provider, NOT panic.
	got = agent.GetProviderForCandidate(providers.FallbackCandidate{Provider: "openai", Model: "gpt-5"})
	if got != primary {
		t.Errorf("GetProviderForCandidate(openai) with no pool entry = %p, want primary %p",
			got, primary)
	}

	// Empty candidate.Provider (legacy wire shape): MUST return primary
	// unconditionally, regardless of pool contents.
	got = agent.GetProviderForCandidate(providers.FallbackCandidate{Provider: "", Model: "anything"})
	if got != primary {
		t.Errorf("GetProviderForCandidate(empty provider) = %p, want primary %p", got, primary)
	}
}

// TestAgentInstance_GetProviderForCandidate_NilAgent ensures the nil-safe
// path used by tests and edge-case callers doesn't panic.
func TestAgentInstance_GetProviderForCandidate_NilAgent(t *testing.T) {
	var agent *AgentInstance
	if got := agent.GetProviderForCandidate(
		providers.FallbackCandidate{Provider: "anthropic", Model: "x"},
	); got != nil {
		t.Errorf("nil agent returned %p, want nil", got)
	}
}

// TestNewAgentInstance_ResolveCandidatesFromDefaultModelPair: an agent with
// no model of its own runs on agents.defaults.default_model — the exact
// (provider, model) pair (ADR-068 D14.1) — and its primary candidate is pinned
// to that pair's provider, never inferred from the slug.
func TestNewAgentInstance_ResolveCandidatesFromDefaultModelPair(t *testing.T) {
	tests := []struct {
		name         string
		provider     string
		modelName    string
		wantProvider string
		wantModel    string
	}{
		{
			// ADR-067 FR-034: a `/` inside a model id is DATA. This id is
			// one OpenRouter model called `stepfun/step-3.5-flash:free`,
			// and it reaches the candidate whole — the old resolver split
			// it and produced the provider `stepfun`, which nothing was
			// configured for.
			name:         "model id containing a slash is never split",
			provider:     "openrouter",
			modelName:    "stepfun/step-3.5-flash:free",
			wantProvider: "openrouter",
			wantModel:    "stepfun/step-3.5-flash:free",
		},
		{
			name:         "bare row model",
			provider:     "openai",
			modelName:    "gpt-4.1",
			wantProvider: "openai",
			wantModel:    "gpt-4.1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDirOuter, err := os.MkdirTemp("", "agent-instance-test-*")
			if err != nil {
				t.Fatalf("Failed to create temp dir: %v", err)
			}
			defer os.RemoveAll(tmpDirOuter)
			// Nested one level below the freshly-made outer container so
			// filepath.Dir(tmpDir) (what NewAgentLoop roots the shared
			// session/task store at) is THIS test's own private tmpDirOuter,
			// never the shared OS temp root — see loop_test.go's
			// newTestAgentLoop doc comment for the leak this closes.
			tmpDir := filepath.Join(tmpDirOuter, "home")
			if err := os.MkdirAll(tmpDir, 0o700); err != nil {
				t.Fatalf("Failed to create nested home dir: %v", err)
			}

			cfg := &config.Config{
				Agents: config.AgentsConfig{
					Defaults: config.AgentDefaults{
						Home:         tmpDir,
						DefaultModel: config.DefaultModel{Provider: tt.provider, Model: tt.modelName},
					},
					List: []config.AgentConfig{{ID: "mia", Home: tmpDir}},
				},
				Providers: []*config.ModelConfig{
					{
						Provider: tt.provider,
						Model:    tt.modelName,
					},
				},
			}

			provider := &mockProvider{}
			agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, provider)

			if len(agent.Candidates) != 1 {
				t.Fatalf("len(Candidates) = %d, want 1", len(agent.Candidates))
			}
			if agent.Candidates[0].Provider != tt.wantProvider {
				t.Fatalf("candidate provider = %q, want %q", agent.Candidates[0].Provider, tt.wantProvider)
			}
			if agent.Candidates[0].Model != tt.wantModel {
				t.Fatalf("candidate model = %q, want %q", agent.Candidates[0].Model, tt.wantModel)
			}
		})
	}
}

func TestNewAgentInstance_AllowsMediaTempDirForReadAndList_RejectsForBash(t *testing.T) {
	workspace := t.TempDir()
	mediaDir := media.TempDir()
	if err := os.MkdirAll(mediaDir, 0o700); err != nil {
		t.Fatalf("MkdirAll(mediaDir) error = %v", err)
	}

	mediaFile, err := os.CreateTemp(mediaDir, "instance-tool-*.txt")
	if err != nil {
		t.Fatalf("CreateTemp(mediaDir) error = %v", err)
	}
	mediaPath := mediaFile.Name()
	if _, err := mediaFile.WriteString("attachment content"); err != nil {
		mediaFile.Close()
		t.Fatalf("WriteString(mediaFile) error = %v", err)
	}
	if err := mediaFile.Close(); err != nil {
		t.Fatalf("Close(mediaFile) error = %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(mediaPath) })

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:                workspace,
				DefaultModel:        config.DefaultModel{Model: "test-model"},
				RestrictToWorkspace: true,
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspace}},
		},
		Tools: config.ToolsConfig{
			ReadFile: config.ReadFileToolConfig{Enabled: true},
			ListDir:  config.ToolConfig{Enabled: true},
			Exec: config.ExecConfig{
				ToolConfig: config.ToolConfig{Enabled: true},
			},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, &mockProvider{})

	readTool, ok := agent.Tools.Get("read_file")
	if !ok {
		t.Fatal("read_file tool not registered")
	}
	readResult := readTool.Execute(context.Background(), map[string]any{"path": mediaPath})
	if readResult.IsError {
		t.Fatalf("read_file should allow media temp dir, got: %s", readResult.ForLLM)
	}
	if !strings.Contains(readResult.ForLLM, "attachment content") {
		t.Fatalf("read_file output missing media content: %s", readResult.ForLLM)
	}

	listTool, ok := agent.Tools.Get("list_directory")
	if !ok {
		t.Fatal("list_directory tool not registered")
	}
	listResult := listTool.Execute(context.Background(), map[string]any{"path": mediaDir})
	if listResult.IsError {
		t.Fatalf("list_directory should allow media temp dir, got: %s", listResult.ForLLM)
	}
	if !strings.Contains(listResult.ForLLM, filepath.Base(mediaPath)) {
		t.Fatalf("list_directory output missing media file: %s", listResult.ForLLM)
	}

	// Corrected 2026-07-04 (7-reviewer gate CRITICAL finding): bash's cwd guard
	// must NOT inherit the read_file/list_directory media-temp-dir allowlist --
	// that allowlist is a deliberate, narrower feature for those two tools only.
	// Prior to the fix, this test asserted the OPPOSITE (bash succeeding here),
	// which was the actual security bug: any agent could `bash cwd=<media dir>`
	// to read another agent's/conversation's uploaded attachments, bypassing the
	// cross-agent guard entirely. This is the real, end-to-end production-wiring
	// proof (via NewAgentInstance's actual buildAllowReadPatterns wiring) that
	// complements the narrower unit test in pkg/tools/bash_test.go
	// (TestBash_CwdRejectsAbsolutePathEvenWhenAllowlisted).
	execTool, ok := agent.Tools.Get("bash")
	if !ok {
		t.Fatal("bash tool not registered")
	}
	execResult := execTool.Execute(context.Background(), map[string]any{
		"command": "cat " + filepath.Base(mediaPath),
		"cwd":     mediaDir,
	})
	if !execResult.IsError {
		t.Fatalf("bash must reject an absolute cwd even when it matches the media-temp-dir allowlist, got success: %s",
			execResult.ForLLM)
	}
	if !strings.Contains(execResult.ForLLM, "path escapes workspace") {
		t.Fatalf("bash rejection should cite the workspace-escape guard, got: %s", execResult.ForLLM)
	}
}

func TestNewAgentInstance_InvalidExecConfigDoesNotExit(t *testing.T) {
	workspace := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         workspace,
				DefaultModel: config.DefaultModel{Model: "test-model"},
			},
			List: []config.AgentConfig{{ID: "mia", Home: workspace}},
		},
		Tools: config.ToolsConfig{
			ReadFile: config.ReadFileToolConfig{Enabled: true},
			Exec: config.ExecConfig{
				ToolConfig: config.ToolConfig{Enabled: true},
			},
		},
	}

	agent := NewAgentInstance(nil, &cfg.Agents.Defaults, cfg, &mockProvider{})
	if agent == nil {
		t.Fatal("expected agent instance, got nil")
	}

	if _, ok := agent.Tools.Get("exec"); ok {
		t.Fatal("exec tool should not be registered when exec config is invalid")
	}

	if _, ok := agent.Tools.Get("read_file"); !ok {
		t.Fatal("read_file tool should still be registered")
	}
}

// TestResolveAgentHome_OMNIPUSHome verifies that resolveAgentHome
// places per-agent workspaces under $OMNIPUS_HOME/agents/ when OMNIPUS_HOME
// is set, and falls back to ~/.omnipus/agents/ when it is not.
func TestResolveAgentHome_OMNIPUSHome(t *testing.T) {
	defaults := &config.AgentDefaults{
		Home: filepath.Join(t.TempDir(), ".omnipus", "workspace"),
	}

	t.Run("OMNIPUS_HOME_set", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("OMNIPUS_HOME", tmpHome)

		agentCfg := &config.AgentConfig{ID: "jim"}
		got := resolveAgentHome(agentCfg, defaults)

		want := filepath.Join(tmpHome, "agents", "jim")
		if got != want {
			t.Errorf("resolveAgentHome with OMNIPUS_HOME=%q: got %q, want %q", tmpHome, got, want)
		}
	})

	t.Run("OMNIPUS_HOME_unset_uses_user_home", func(t *testing.T) {
		t.Setenv("OMNIPUS_HOME", "")
		fakeHome := t.TempDir()
		t.Setenv("HOME", fakeHome)

		agentCfg := &config.AgentConfig{ID: "ava"}
		got := resolveAgentHome(agentCfg, defaults)

		want := filepath.Join(fakeHome, ".omnipus", "agents", "ava")
		if got != want {
			t.Errorf("resolveAgentHome without OMNIPUS_HOME: got %q, want %q", got, want)
		}
	})

	t.Run("path_traversal_guarded", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("OMNIPUS_HOME", tmpHome)

		agentCfg := &config.AgentConfig{ID: "../../etc/passwd"}
		got := resolveAgentHome(agentCfg, defaults)

		safeBase := filepath.Join(tmpHome, "agents")
		if !strings.HasPrefix(filepath.Clean(got), safeBase) {
			t.Errorf("path traversal not guarded: got %q, expected prefix %q", got, safeBase)
		}
	})

	// F2: a routing-default agent (Default=true) must still get its own
	// per-agent workspace under agents/{id}/, not the shared default workspace.
	t.Run("routing_default_gets_own_workspace", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("OMNIPUS_HOME", tmpHome)

		// Default=true means "routing default" — it must NOT redirect to shared workspace.
		agentCfg := &config.AgentConfig{ID: "mia", Default: true}
		got := resolveAgentHome(agentCfg, defaults)

		want := filepath.Join(tmpHome, "agents", "mia")
		if got != want {
			t.Errorf("Default=true agent workspace: got %q, want %q (must be per-agent, not shared)", got, want)
		}

		// The shared workspace must NOT be returned.
		if got == defaults.Home {
			t.Errorf("Default=true agent must not receive the shared default workspace %q", defaults.Home)
		}
	})
}

// TestResolveAgentHome_ExportedWrapper proves ResolveAgentHome (the
// exported wrapper added for pkg/gateway's executor-smoke-test agent_id
// feature — see its doc) delegates to the exact same resolveAgentHome
// this file already covers above, for both the explicit-Home and the
// per-agent-derived-path cases, so a future edit to one cannot silently
// diverge from the other.
func TestResolveAgentHome_ExportedWrapper(t *testing.T) {
	defaults := &config.AgentDefaults{
		Home: filepath.Join(t.TempDir(), ".omnipus", "workspace"),
	}

	t.Run("explicit_workspace_passthrough", func(t *testing.T) {
		explicit := filepath.Join(t.TempDir(), "agents", "custom-explicit")
		agentCfg := &config.AgentConfig{ID: "custom-explicit", Home: explicit}

		got := ResolveAgentHome(agentCfg, defaults)
		want := resolveAgentHome(agentCfg, defaults)
		if got != want {
			t.Errorf("ResolveAgentHome diverged from resolveAgentHome: got %q, want %q", got, want)
		}
		if got != explicit {
			t.Errorf("ResolveAgentHome with explicit Home: got %q, want %q", got, explicit)
		}
	})

	t.Run("derived_per_agent_path", func(t *testing.T) {
		tmpHome := t.TempDir()
		t.Setenv("OMNIPUS_HOME", tmpHome)

		agentCfg := &config.AgentConfig{ID: "ray"}
		got := ResolveAgentHome(agentCfg, defaults)
		want := resolveAgentHome(agentCfg, defaults)
		if got != want {
			t.Errorf("ResolveAgentHome diverged from resolveAgentHome: got %q, want %q", got, want)
		}
		wantPath := filepath.Join(tmpHome, "agents", "ray")
		if got != wantPath {
			t.Errorf("ResolveAgentHome derived path: got %q, want %q", got, wantPath)
		}
	})
}
