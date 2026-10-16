package agent

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/elicify-ai/omnipus/pkg/steer"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// #1274: fixtures use the same system-tool registration, entity roster refresh
// and fast publication primitives as gateway.go::wireSystemTools. The only fake
// here is the network boundary. No test calls the real developer's CLI or home.
const l11274WorkspaceID = "01K7A1274000000000000000000" // Isolated default workspace fixture, not product data.

// The model is supplied as input, not inferred from observed output. #1274 tests
// executor routing rather than model-provider authentication or WU-24 removal.
const l11274Model = "test-model" // No provider prefix: use the injected, no-network provider.
const l11274Soul = "You are a delegation-only worker. Complete the assigned task."

type l11274Provider struct{ calls atomic.Int64 }

func (p *l11274Provider) Chat(context.Context, []providers.Message, []providers.ToolDefinition, string, map[string]any) (*providers.LLMResponse, error) {
	p.calls.Add(1)
	return &providers.LLMResponse{Content: "native control completed"}, nil
}

func (*l11274Provider) GetDefaultModel() string { return l11274Model }

type l11274Fixture struct {
	home       string
	configPath string
	al         *AgentLoop
	provider   *l11274Provider
	ctx        context.Context
}

func newL11274Fixture(t *testing.T, ceiling string, agentDeny bool) *l11274Fixture {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(config.EnvHome, home)
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.DefaultModel = config.DefaultModel{Model: l11274Model}
	coreagent.SeedConfig(cfg)
	if ceiling != "" {
		cfg.Sandbox.ToolPolicies["create_agent"] = ceiling
	}
	if agentDeny {
		for i := range cfg.Agents.List {
			if cfg.Agents.List[i].ID == "omnipus" {
				cfg.Agents.List[i].Tools.Builtin.Policies["create_agent"] = config.ToolPolicyDeny
			}
		}
	}
	store := agentstore.New(home)
	for i := range cfg.Agents.List {
		id := cfg.Agents.List[i].ID
		if _, err := store.CreateState(id, &cfg.Agents.List[i], "Standing test fixture for "+id); err != nil {
			t.Fatalf("SETUP: persist seeded agent %s: %v", id, err)
		}
	}
	l11274WriteWorkspace(t, home)
	configPath := filepath.Join(home, "config.json")
	if err := config.SaveConfig(configPath, cfg); err != nil {
		t.Fatalf("SETUP: save real config: %v", err)
	}
	provider := &l11274Provider{}
	// Do not use mustNewAgentLoop: it auto-seeds membership and a full mesh,
	// which could conceal a broken create_agent join or delegation gate.
	al, err := NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	if err != nil {
		t.Fatalf("SETUP: NewAgentLoop: %v", err)
	}
	t.Cleanup(al.Close)
	al.SetSessionMessagingStores(session.NewMessageInboxStore(filepath.Join(home, "session_messages")),
		session.NewLifecycleStore(filepath.Join(home, "session_lifecycle")))
	bootDir := filepath.Join(home, "boot_epoch")
	if mkErr := os.MkdirAll(bootDir, 0o700); mkErr != nil {
		t.Fatalf("SETUP: create boot epoch directory: %v", mkErr)
	}
	boot := session.NewBootEpochStore(bootDir)
	if _, mintErr := boot.Mint(); mintErr != nil {
		t.Fatalf("SETUP: mint boot epoch: %v", mintErr)
	}
	al.SetBootEpochStore(boot)
	// Gateway boot explicitly injects these; NewAgentLoop alone deliberately
	// leaves delegate fail-closed. Use the real implementations, not stubs.
	classifier := NewSteerRecordClassifier(al.GetSessionLifecycleStore(), al.GetSessionStore())
	al.SetSteerAudienceDeps(NewSteerAudienceResolver(classifier), steer.NopBoundaryObserver{}, NewSteerUpwardDeliverer())
	al.SetSteerSessionLauncher(NewSteerLauncher(al))
	deps := &systools.Deps{
		Home: home, ConfigPath: configPath, GetCfg: al.GetConfig,
		MutateConfig:        al.MutateConfig,
		SaveConfigLocked:    func(c *config.Config) error { return config.SaveConfig(configPath, c) },
		UpsertAgentFastFunc: func(id string) error { return l11274Publish(al, home, id) },
		DeleteSessionStore:  func() systools.AgentSessionStore { return al.GetSessionStore() },
	}
	al.WireSysagentDeps(deps)
	main, err := al.GetSessionStore().GetOrCreateMainSession(l11274WorkspaceID, "omnipus")
	if err != nil {
		t.Fatalf("SETUP: Omnipus's real main: %v", err)
	}
	ctx := tools.WithWorkspaceID(context.Background(), l11274WorkspaceID)
	ctx = tools.WithTranscriptSessionID(ctx, main.ID)
	ctx = tools.WithSessionKey(ctx, main.ID)
	ctx = tools.WithSessionOwner(ctx, "l11274-tester")
	return &l11274Fixture{home: home, configPath: configPath, al: al, provider: provider, ctx: ctx}
}

func l11274WriteWorkspace(t *testing.T, home string) {
	t.Helper()
	w := workspace.Workspace{ID: l11274WorkspaceID, Name: "1274 test workspace", Status: "active",
		IsDefault: true, CoreTeam: []string{"omnipus"}}
	data, err := json.Marshal(w)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, "workspaces")
	if err := os.MkdirAll(filepath.Join(dir, l11274WorkspaceID, "work"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, l11274WorkspaceID+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := workspace.SaveDelegation(home, l11274WorkspaceID, nil); err != nil {
		t.Fatalf("SETUP: empty delegation store: %v", err)
	}
}

func l11274Publish(al *AgentLoop, home, id string) error {
	if err := al.MutateConfig(func(c *config.Config) error {
		agents, skipped, err := agentstore.New(home).List()
		if err != nil {
			return err
		}
		c.Agents.List, c.SkippedAgentIDs = agents, skipped
		return nil
	}); err != nil {
		return err
	}
	_, err := al.UpsertAgentFast(al.GetConfig(), id)
	return err
}

func (f *l11274Fixture) execute(t *testing.T, name string, args map[string]any) *tools.ToolResult {
	t.Helper()
	omnipus, ok := f.al.GetRegistry().GetAgent("omnipus")
	if !ok || omnipus == nil {
		t.Fatal("BLOCKED: Omnipus not implemented — required by #1274 / spec FR-001")
	}
	tool, ok := omnipus.Tools.Get(name)
	if !ok {
		t.Fatalf("BLOCKED: %s is not registered on Omnipus — required by #1274 / spec §6.5", name)
	}
	if policy := tools.EffectiveToolPolicy(omnipus.LoadToolPolicy(), tool.Scope(), omnipus.AgentType, name); policy != "allow" {
		t.Fatalf("#1274: Omnipus must reach %s at the default allow ceiling, got %q", name, policy)
	}
	// ExecuteWithContext includes the model-facing argument validator; using
	// NewAgentCreateTool.Execute directly would miss that real entry boundary.
	return omnipus.Tools.ExecuteWithContext(tools.WithToolCallID(f.ctx, "1274-"+name), name, args, "webchat", "1274-chat", nil)
}

func l11274CreateArgs(name, cli, path string) map[string]any {
	// Chief clarification Q1 B, 2026-10-14: external workers have no native
	// soul. Omitting it is the valid request; it remains required for natives.
	return map[string]any{"name": name, "description": "External worker for issue 1274",
		"model": l11274Model, "color": "#6E8BA8", "agent_type": "subagent_3p", "cli": cli, "cli_path": path}
}

func l11274Executable(t *testing.T, home, cli string, executable bool) string {
	t.Helper()
	// Spaces exercise legitimate non-standard install paths, without relying
	// on the user's PATH or on any installed external agent.
	dir := filepath.Join(home, "fake CLI installs")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	name, content, mode := cli, "#!/bin/sh\nprintf '9.9.9\\n'\n", os.FileMode(0o700)
	if runtime.GOOS == "windows" {
		// A controlled executable-extension policy: .cmd is permitted, .txt
		// is not. Do not inherit an operator's custom .txt association.
		t.Setenv("PATHEXT", ".COM;.EXE;.BAT;.CMD")
		name, content = name+".cmd", "@echo off\r\necho 9.9.9\r\n"
	}
	if !executable {
		name, content, mode = cli+".txt", "Plain data, not an external CLI program.\n", 0o600
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	// CHECK F02: Windows LookPath accepts an existing .txt file. It is not
	// a negative executable oracle: return the data fixture to the product.
	if executable || runtime.GOOS != "windows" {
		// Chief Q1 A / re-CHECK R01: restore the original assertion exactly
		// on Unix. Windows alone is exempt from the negative lookup check.
		if executable {
			if _, err := exec.LookPath(path); err != nil {
				t.Fatalf("SETUP: positive executable-path instrument: %v", err)
			}
		} else if _, err := exec.LookPath(path); err == nil {
			t.Fatalf("SETUP: non-executable fixture %q unexpectedly passes executable lookup", path)
		}
	}
	return path
}

func l11274ResultObject(t *testing.T, result *tools.ToolResult) map[string]any {
	t.Helper()
	if result == nil {
		t.Fatal("tool returned no result")
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(result.ForLLM), &body); err != nil {
		t.Fatalf("model-facing result is not its JSON envelope: %v; ForLLM=%q", err, result.ForLLM)
	}
	return body
}

// l11274AssertAbsent proves a refused create persisted and published nothing
// for the named request. key is the request's name in the hyphenated
// lowercase form these tests were written with; since new agents get
// generated UUID ids (founder decision 2026-10-14, WU-3) the check looks for
// any stored agent whose NAME has that form, rather than for an entity at a
// name-derived id that could never exist.
func l11274AssertAbsent(t *testing.T, f *l11274Fixture, key string) {
	t.Helper()
	if _, err := agentstore.New(f.home).Get(key); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("refused create must leave no entity %q, got %v", key, err)
	}
	agents, _, err := agentstore.New(f.home).List()
	if err != nil {
		t.Fatalf("list the real entity store: %v", err)
	}
	for _, a := range agents {
		if l11274NameKey(a.Name) != key {
			continue
		}
		t.Errorf("refused create persisted agent %q (name %q)", a.ID, a.Name)
		if _, ok := f.al.GetRegistry().GetAgent(a.ID); ok {
			t.Errorf("refused create published live agent %q (name %q)", a.ID, a.Name)
		}
	}
}

// l11274NameKey is the hyphenated lowercase form of a name the refusal tests
// use as a request key (test-only; production never derives an id from it).
func l11274NameKey(name string) string {
	var b strings.Builder
	hyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			hyphen = false
		} else if !hyphen && b.Len() > 0 {
			b.WriteByte('-')
			hyphen = true
		}
	}
	return strings.TrimSuffix(b.String(), "-")
}

func l11274FailureDetails(t *testing.T, result *tools.ToolResult, wantCode string) string {
	t.Helper()
	body := l11274ResultObject(t, result)
	if !result.IsError || body["success"] != false {
		t.Fatalf("expected %s refusal, got IsError=%v, ForLLM=%s", wantCode, result.IsError, result.ForLLM)
	}
	errBody, ok := body["error"].(map[string]any)
	if !ok || errBody["code"] != wantCode {
		t.Fatalf("refusal code = %v, want %q; ForLLM=%s", body["error"], wantCode, result.ForLLM)
	}
	message, ok := errBody["message"].(string)
	if !ok || message == "" {
		t.Fatalf("refusal lost the reason the model sees: %s", result.ForLLM)
	}
	return message
}
