// Omnipus — System Agent Tool Tests
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package systools_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
	"github.com/elicify-ai/omnipus/pkg/providers"
	"github.com/elicify-ai/omnipus/pkg/routing"
	systools "github.com/elicify-ai/omnipus/pkg/sysagent/tools"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// testMutateConfig is a simple mutex-serialized MutateConfig for use in tests
// that do not have a real AgentLoop. It serializes concurrent calls exactly as
// AgentLoop.MutateConfig does, making -race tests valid.
func testMutateConfig(mu *sync.Mutex, getCfg func() *config.Config) func(fn func(*config.Config) error) error {
	return func(fn func(*config.Config) error) error {
		mu.Lock()
		defer mu.Unlock()
		return fn(getCfg())
	}
}

// newTestDeps creates a minimal Deps for agent tool unit tests.
// GetCfg returns the captured pointer; SaveConfig is a no-op so callers
// can verify in-memory state without touching disk.
//
// ADR-054: create_agent/update_agent/delete_agent persist real entity files
// under Home/entities/agents/ (agentstore) now — Home MUST be an
// isolated-per-call directory. The historical shared fixed path
// ("/tmp/omnipus-test") would leak entity files across test functions within
// the same test binary run, causing order-dependent AGENT_ALREADY_EXISTS /
// AGENT_NOT_FOUND failures. newTestDeps has no *testing.T (many call sites
// predate this helper's use in agent-store-backed tests), so it cannot use
// t.TempDir(); os.MkdirTemp gives each call its own unique directory instead
// — never cleaned up automatically, but negligible (a handful of small JSON
// files) for an ephemeral test run.
func newTestDeps() (*systools.Deps, *config.Config) {
	cfg := config.DefaultConfig()
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	home, err := os.MkdirTemp("", "omnipus-agent-test-*")
	if err != nil {
		home = "/tmp/omnipus-test"
	}
	deps := &systools.Deps{
		Home:         home,
		ConfigPath:   filepath.Join(home, "config.json"),
		GetCfg:       getCfg,
		MutateConfig: testMutateConfig(&mu, getCfg),
		// SaveConfig is a no-op in unit tests — we inspect cfg directly.
		SaveConfigLocked: func(cfg *config.Config) error { return nil },
		CredStore:        nil,
	}
	return deps, cfg
}

// newTestDepsWithRealSave creates Deps backed by a real temp-dir config.json.
// Use this to catch JSON serialization regressions (missing tags, etc.).
func newTestDepsWithRealSave(t *testing.T) (*systools.Deps, string) {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := config.DefaultConfig()
	if err := config.SaveConfig(cfgPath, cfg); err != nil {
		t.Fatalf("newTestDepsWithRealSave: seed config: %v", err)
	}
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	deps := &systools.Deps{
		Home:             dir,
		ConfigPath:       cfgPath,
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: func(cfg *config.Config) error { return config.SaveConfig(cfgPath, cfg) },
		CredStore:        nil,
	}
	return deps, cfgPath
}

// parseSuccess unmarshals the tool result body into a map. Fails the test if
// the body is not valid JSON. Success responses from successJSON do not include
// a "success" field — they are the data object directly. Error responses (from
// errorJSON) do include "success":false. We only fail if "success" is explicitly
// false.
func parseSuccess(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("result body is not valid JSON: %v\nbody: %s", err, body)
	}
	// errorJSON sets success=false explicitly; a missing key means success.
	if success, ok := m["success"]; ok {
		if b, _ := success.(bool); !b {
			t.Fatalf("expected success, got error response: %s", body)
		}
	}
	return m
}

// parseError unmarshals the tool result body into a map and asserts success is
// explicitly false (as produced by errorJSON).
func parseError(t *testing.T, body string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil {
		t.Fatalf("result body is not valid JSON: %v\nbody: %s", err, body)
	}
	if success, ok := m["success"]; !ok {
		t.Fatalf("expected error response with success=false, got: %s", body)
	} else if b, _ := success.(bool); b {
		t.Fatalf("expected success=false, got success=true: %s", body)
	}
	return m
}

// newTestDepsWithHome creates a minimal Deps that uses a real temp-dir as Home
// (so workspace file paths are real). No config persistence.
func newTestDepsWithHome(t *testing.T) (*systools.Deps, string) {
	t.Helper()
	home := t.TempDir()
	cfg := config.DefaultConfig()
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	deps := &systools.Deps{
		Home:             home,
		ConfigPath:       filepath.Join(home, "config.json"),
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: func(cfg *config.Config) error { return nil },
		CredStore:        nil,
	}
	return deps, home
}

// TestAgentCreate_WithColorAndIcon verifies that create persists color and icon
// into the AgentConfig in-memory and returns them in the response.
//
// Traces to: wave5b-system-agent-spec.md — BRD §D.4.2 agent.create
func TestAgentCreate_WithColorAndIcon(t *testing.T) {
	deps, _ := newTestDeps()
	tool := systools.NewAgentCreateTool(deps)

	result := tool.Execute(context.Background(), map[string]any{
		"name":        "Research Bot",
		"description": "A research assistant",
		"soul":        "You are a research bot.",
		"model":       "test/model",
		"color":       "#22C55E",
		"icon":        "robot",
	})

	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}

	// ADR-054: verify the persisted entity record (entities/agents/<id>.json)
	// via the agent store, not cfg.Agents.List — create_agent no longer
	// touches the in-memory config's roster at all.
	agent, err := agentstore.New(deps.Home).Get("research-bot")
	if err != nil {
		t.Fatalf("expected agent entity record to exist: %v", err)
	}
	if agent.ID != "research-bot" {
		t.Errorf("ID = %q, want %q", agent.ID, "research-bot")
	}
	if agent.Color != "#22C55E" {
		t.Errorf("Color = %q, want %q", agent.Color, "#22C55E")
	}
	if agent.Icon != "robot" {
		t.Errorf("Icon = %q, want %q", agent.Icon, "robot")
	}
}

// TestAgentDelete_RequiresConfirm verifies that delete without confirm=true is
// rejected, and with confirm=true the agent is removed from the list.
//
// Traces to: wave5b-system-agent-spec.md — BRD §D.4.2 agent.delete
func TestAgentDelete_RequiresConfirm(t *testing.T) {
	deps, _ := newTestDeps()
	// ADR-054: pre-populate a REAL entity record (entities/agents/my-agent.json)
	// — delete_agent now resolves against the agent store, not cfg.Agents.List.
	store := agentstore.New(deps.Home)
	if err := store.Create("my-agent", &config.AgentConfig{ID: "my-agent", Name: "My Agent"}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}

	tool := systools.NewAgentDeleteTool(deps)

	// Without confirm — must fail.
	resultNoConfirm := tool.Execute(context.Background(), map[string]any{
		"id":      "my-agent",
		"confirm": false,
	})
	if !resultNoConfirm.IsError {
		t.Fatal("expected error when confirm=false, got success")
	}
	parseError(t, resultNoConfirm.ForLLM)
	if _, err := store.Get("my-agent"); err != nil {
		t.Fatalf("agent should not be deleted when confirm=false: Get failed: %v", err)
	}

	// With confirm=true — must succeed and remove agent.
	resultConfirmed := tool.Execute(context.Background(), map[string]any{
		"id":      "my-agent",
		"confirm": true,
	})
	if resultConfirmed.IsError {
		t.Fatalf("expected success with confirm=true, got error: %s", resultConfirmed.ForLLM)
	}
	parseSuccess(t, resultConfirmed.ForLLM)
	if _, err := store.Get("my-agent"); !errors.Is(err, entity.ErrNotFound) {
		t.Errorf("expected agent entity record to be deleted, Get error = %v", err)
	}
}

// TestAgentUpdate_PartialFields verifies that updating only `name` does not
// clobber color and icon already set on the agent.
//
// Traces to: wave5b-system-agent-spec.md — BRD §D.4.2 agent.update
func TestAgentUpdate_PartialFields(t *testing.T) {
	deps, _ := newTestDeps()
	// ADR-054: pre-populate a REAL entity record — update_agent now resolves
	// against the agent store, not cfg.Agents.List.
	store := agentstore.New(deps.Home)
	if err := store.Create("my-agent", &config.AgentConfig{
		ID:    "my-agent",
		Name:  "Old Name",
		Color: "#FF0000",
		Icon:  "star",
	}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}

	tool := systools.NewAgentUpdateTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"id":   "my-agent",
		"name": "New Name",
	})

	if result.IsError {
		t.Fatalf("update failed: %s", result.ForLLM)
	}

	agent, err := store.Get("my-agent")
	if err != nil {
		t.Fatalf("read back updated agent entity record: %v", err)
	}
	if agent.Name != "New Name" {
		t.Errorf("Name = %q, want %q", agent.Name, "New Name")
	}
	// Color and Icon must be unchanged.
	if agent.Color != "#FF0000" {
		t.Errorf("Color changed to %q; expected %q", agent.Color, "#FF0000")
	}
	if agent.Icon != "star" {
		t.Errorf("Icon changed to %q; expected %q", agent.Icon, "star")
	}
}

// TestAgentCreate_PersistsToDisk verifies that create writes color and icon to
// disk. Catches JSON-tag typos and pointer-marshaling regressions.
//
// ADR-054: agents are per-entity records under entities/agents/<id>.json now,
// not config.json's agents.list — this pins the NEW on-disk location. A
// sibling assertion (config.json itself carries no agents.list content after
// the create) proves the ADR's headline benefit: creating an agent no longer
// touches config.json at all.
//
// Traces to: wave5b-system-agent-spec.md — BRD §D.4.2 agent.create (persistence)
func TestAgentCreate_PersistsToDisk(t *testing.T) {
	deps, cfgPath := newTestDepsWithRealSave(t)

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Disk Bot",
		"description": "A disk bot",
		"soul":        "You are a disk bot.",
		"model":       "test/model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	if result.IsError {
		t.Fatalf("create failed: %s", result.ForLLM)
	}

	entityPath := filepath.Join(deps.Home, "entities", "agents", "disk-bot.json")
	data, err := os.ReadFile(entityPath)
	if err != nil {
		t.Fatalf("read agent entity record %s: %v", entityPath, err)
	}
	var entry map[string]any
	if unmarshalErr := json.Unmarshal(data, &entry); unmarshalErr != nil {
		t.Fatalf("unmarshal agent entity record: %v", unmarshalErr)
	}
	if entry["color"] != "#22C55E" {
		t.Errorf("disk color = %v, want #22C55E", entry["color"])
	}
	if entry["icon"] != "robot" {
		t.Errorf("disk icon = %v, want robot", entry["icon"])
	}

	// config.json itself must carry no agents.list content.
	cfgData, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(cfgData, &raw); err != nil {
		t.Fatalf("unmarshal config.json: %v", err)
	}
	if agentsSection, ok := raw["agents"].(map[string]any); ok {
		if list, ok := agentsSection["list"].([]any); ok && len(list) > 0 {
			t.Errorf("config.json agents.list = %v, want empty — agents must persist only to entities/agents/", list)
		}
	}
}

// TestAgentDelete_RefusesLockedAgent verifies locked (core) agents cannot be deleted.
// The guard is now based on the Locked field, not on a hardcoded agent ID (FR-045).
//
// Traces to: architect finding #3 — self-deactivation guard
func TestAgentDelete_RefusesLockedAgent(t *testing.T) {
	deps, _ := newTestDeps()
	// ADR-054: seed a locked core agent as a REAL entity record — delete_agent
	// now resolves the Locked check against the agent store, not
	// cfg.Agents.List.
	store := agentstore.New(deps.Home)
	if err := store.Create("locked-core", &config.AgentConfig{
		ID:     "locked-core",
		Name:   "Locked Core",
		Locked: true,
	}); err != nil {
		t.Fatalf("test setup: create agent entity record: %v", err)
	}
	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "locked-core",
		"confirm": true,
	})
	if !result.IsError {
		t.Fatal("expected error when deleting locked agent, got success")
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	// Anti-shortcut: AgentDeleteTool.Execute has SIX distinct error returns,
	// and FOUR of them satisfy a bare "code != nil" check — AGENT_NOT_FOUND
	// (store.Get miss, e.g. a fixture pointed at the wrong Home), two other
	// SAVE_FAILED variants (a non-ErrNotFound store.Get error, or a
	// store.Delete failure), and the intended locked rejection (also
	// SAVE_FAILED). A regression that resolved this test's store against the
	// wrong Home would yield AGENT_NOT_FOUND — non-nil, AND the seeded
	// record would trivially "survive" (it was never visible to the tool in
	// the first place) — passing both this check and the survival check
	// below while the locked-agent guard is never actually exercised. Assert
	// the exact code AND that the message names the locked-core-agent reason.
	if errBlock["code"] != "SAVE_FAILED" {
		t.Errorf("expected error code SAVE_FAILED, got %v", errBlock["code"])
	}
	msg, _ := errBlock["message"].(string)
	if !strings.Contains(msg, "locked core agent") {
		t.Errorf("expected message naming the locked-core-agent rejection, got %q", msg)
	}
	// The agent must still exist — the locked check must run BEFORE delete.
	if _, err := store.Get("locked-core"); err != nil {
		t.Errorf("locked agent entity record must survive a rejected delete, Get error = %v", err)
	}
}

// TestAgentCreate_RejectsInvalidColor verifies that invalid hex colors are rejected.
func TestAgentCreate_RejectsInvalidColor(t *testing.T) {
	deps, _ := newTestDeps()
	for _, bad := range []string{"red", "#GGGGGG", "#12345", "22C55E", "#22C55E00"} {
		result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
			"name":        "Bot",
			"description": "A test bot",
			"soul":        "You are a test bot.",
			"model":       "test/model",
			"color":       bad,
		})
		if !result.IsError {
			t.Errorf("create with color=%q should fail, got success", bad)
		}
		m := parseError(t, result.ForLLM)
		errBlock, _ := m["error"].(map[string]any)
		if errBlock["code"] != "INVALID_COLOR" {
			t.Errorf("color=%q: code = %v, want INVALID_COLOR", bad, errBlock["code"])
		}
	}
}

// TestAgentCreate_RejectsInvalidIcon verifies that invalid icon names are rejected.
func TestAgentCreate_RejectsInvalidIcon(t *testing.T) {
	deps, _ := newTestDeps()
	for _, bad := range []string{"my icon", "icon!", "icon/sub", "icon..bad"} {
		result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
			"name":        "Bot",
			"description": "A test bot",
			"soul":        "You are a test bot.",
			"model":       "test/model",
			"icon":        bad,
		})
		if !result.IsError {
			t.Errorf("create with icon=%q should fail, got success", bad)
		}
		m := parseError(t, result.ForLLM)
		errBlock, _ := m["error"].(map[string]any)
		if errBlock["code"] != "INVALID_ICON" {
			t.Errorf("icon=%q: code = %v, want INVALID_ICON", bad, errBlock["code"])
		}
	}
}

// TestAgentUpdate_RejectsInvalidColor verifies update validates color.
func TestAgentUpdate_RejectsInvalidColor(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Agents.List = []config.AgentConfig{{ID: "my-agent", Name: "My Agent"}}
	result := systools.NewAgentUpdateTool(deps).Execute(context.Background(), map[string]any{
		"id":    "my-agent",
		"color": "not-a-color",
	})
	if !result.IsError {
		t.Fatal("update with invalid color should fail")
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "INVALID_COLOR" {
		t.Errorf("code = %v, want INVALID_COLOR", errBlock["code"])
	}
}

// NOTE: This test uses a stub MutateConfig backed by sync.Mutex to validate
// the WithConfig internal serialization contract. It does NOT exercise the
// real AgentLoop.MutateConfig / REST GetConfig RWMutex. Cross-subsystem
// race coverage is validated by the integration test TestConcurrentRESTAndSysagentConfigWrite
// in this file. Run with -race to catch data races within the WithConfig/MutateConfig boundary.
func TestWithConfig_SerializesReaderWriter(t *testing.T) {
	cfg := config.DefaultConfig()
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }

	deps := &systools.Deps{
		Home:             t.TempDir(),
		ConfigPath:       filepath.Join(t.TempDir(), "config.json"),
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: func(cfg *config.Config) error { return nil },
		CredStore:        nil,
	}
	createTool := systools.NewAgentCreateTool(deps)

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// wg tracks every writer/reader goroutine below so the test can block
	// until each has actually RETURNED from its loop — not merely until ctx
	// has been signaled Done. <-ctx.Done() alone (the test's previous final
	// line) only waits for the DEADLINE to fire; it says nothing about
	// whether a goroutine's in-flight createTool.Execute call (which writes
	// real agent-entity files under deps.Home == t.TempDir()) has finished
	// and the goroutine has looped back around to observe ctx.Done() and
	// return. Without this wg, the test function could return — and
	// t.TempDir()'s cleanup could call os.RemoveAll — while a writer
	// goroutine was still mid-write into the entities/ subtree, racing a
	// "TempDir RemoveAll cleanup: directory not empty" failure (the same
	// defect class as TestDelegate_StatusReflectsRealState in pkg/tools and
	// the TaskExecutor goal-loop drain in pkg/agent — an unwaited background
	// goroutine outliving the test function).
	var wg sync.WaitGroup

	// Writers: call system.agent.create concurrently.
	const numWriters = 4
	wg.Add(numWriters)
	for i := range numWriters {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					createTool.Execute(ctx, map[string]any{
						"name":        fmt.Sprintf("Bot %d", i),
						"description": "A test bot",
						"soul":        "You are a test bot.",
						"model":       "test/model",
						"color":       "#22C55E",
						"icon":        "robot",
					})
				}
			}
		}()
	}

	// Readers: read cfg.Agents.List via MutateConfig (same lock path as REST
	// handlers that call GetConfig → RLock). Here we simulate a concurrent
	// reader by acquiring the mutex in read mode via a secondary lock.
	const numReaders = 4
	wg.Add(numReaders)
	for range numReaders {
		go func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				default:
					// Simulate REST reader: reads under same mutex as MutateConfig.
					// In production, REST uses al.mu.RLock via GetConfig(); here
					// testMutateConfig uses a plain Mutex for simplicity, so readers
					// must also acquire it to exercise the race detector.
					mu.Lock()
					_ = len(getCfg().Agents.List)
					mu.Unlock()
				}
			}
		}()
	}

	<-ctx.Done()
	wg.Wait()
}

// TestSystemConfigSet_RollbackOnSaveFailure verifies that system.config.set
// rolls back the in-memory config when SaveConfig fails.
//
// Traces to: Blocker 2 — system.config.set must use WithConfig for rollback.
func TestSystemConfigSet_RollbackOnSaveFailure(t *testing.T) {
	cfg := config.DefaultConfig()
	originalPort := cfg.Gateway.Port
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }

	deps := &systools.Deps{
		Home:             t.TempDir(),
		ConfigPath:       filepath.Join(t.TempDir(), "config.json"),
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: func(cfg *config.Config) error { return errors.New("disk full") },
		CredStore:        nil,
	}

	tool := systools.NewConfigSetTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"key":   "gateway.port",
		"value": float64(9999),
	})

	if !result.IsError {
		t.Fatal("expected error on save failure, got success")
	}
	// Port must be rolled back to original value.
	if cfg.Gateway.Port != originalPort {
		t.Errorf("gateway.port not rolled back: got %v, want %v", cfg.Gateway.Port, originalPort)
	}
}

// TestWithConfig_MapFieldRollback verifies that when fn adds a new key to a
// map field on the config and then returns an error, WithConfig fully removes
// that key on rollback. This guards against the Go stdlib json.Unmarshal
// map-merge behavior: Unmarshal into a non-nil map extends it rather than
// replacing it, so restoreConfig must clear all maps before unmarshaling the
// snapshot.
//
// Traces to: silent-failure-hunter round 3 finding #2.
func TestWithConfig_MapFieldRollback(t *testing.T) {
	cfg := config.DefaultConfig()
	// Ensure the map is initialized but empty before the test.
	cfg.ChannelPolicies = map[string]config.OmnipusChannelPolicy{}

	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	deps := &systools.Deps{
		Home:         t.TempDir(),
		ConfigPath:   filepath.Join(t.TempDir(), "config.json"),
		GetCfg:       getCfg,
		MutateConfig: testMutateConfig(&mu, getCfg),
		// SaveConfig is a no-op — we are testing rollback on fn error, not save error.
		SaveConfigLocked: func(cfg *config.Config) error { return nil },
		CredStore:        nil,
	}

	sentinelErr := errors.New("fn deliberately failed")
	err := deps.WithConfig(func(cfg *config.Config) error {
		// Add a new key to the map.
		cfg.ChannelPolicies["injected-channel"] = config.OmnipusChannelPolicy{}
		return sentinelErr
	})

	if !errors.Is(err, sentinelErr) {
		t.Fatalf("expected sentinelErr, got: %v", err)
	}
	if _, found := cfg.ChannelPolicies["injected-channel"]; found {
		t.Error("map key 'injected-channel' survived rollback; restoreConfig map-clear is broken")
	}
}

// TestConcurrentRESTAndSysagentConfigWrite exercises the concurrency contract
// between the REST safeUpdateConfigJSON path and the sysagent WithConfig path.
// Both paths ultimately write to the same config; the test verifies that
// concurrent calls complete within a tight deadline (no deadlock) and that the
// race detector reports no data races.
//
// This is an integration-level stub using the stub MutateConfig (not the real
// AgentLoop). True cross-subsystem deadlock detection requires the real
// AgentLoop and is covered at the system-test level.
//
// Must be run with -race to be useful: go test -race ./pkg/sysagent/...
func TestConcurrentRESTAndSysagentConfigWrite(t *testing.T) {
	cfg := config.DefaultConfig()
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }

	// restConfigMu simulates the REST layer's configMu.
	var restConfigMu sync.Mutex

	deps := &systools.Deps{
		Home:             t.TempDir(),
		ConfigPath:       filepath.Join(t.TempDir(), "config.json"),
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: func(cfg *config.Config) error { return nil },
		CredStore:        nil,
	}
	createTool := systools.NewAgentCreateTool(deps)

	const deadline = 5 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			default:
				// Simulate REST safeUpdateConfigJSON: acquires restConfigMu, then
				// reads and writes cfg fields under mu (via MutateConfig).
				restConfigMu.Lock()
				_ = deps.WithConfig(func(cfg *config.Config) error {
					cfg.Gateway.Port = 8080
					return nil
				})
				restConfigMu.Unlock()
			}
		}
	}()

	// Sysagent path: acquires mu via MutateConfig (inside createTool.Execute →
	// WithConfig). Never acquires restConfigMu — no deadlock possible.
	for {
		select {
		case <-ctx.Done():
			<-done
			return
		default:
			createTool.Execute(
				ctx,
				map[string]any{
					"name":        "concurrent-bot",
					"description": "test",
					"soul":        "test",
					"model":       "test/model",
					"color":       "#22C55E",
					"icon":        "robot",
				},
			)
		}
	}
}

// TestAgentCreateUpdate_ContentOnly_NoMetadataToolBypass verifies that
// system.agent.create and system.agent.update write content-only to SOUL.md
// and HEARTBEAT.md (no extra frontmatter injection, no extra files).
//
// This locks the content-only invariant per issue #240: the create/update paths
// are the canonical write path for soul/heartbeat content. agent.write_metadata
// must not change this — both paths must produce byte-for-byte identical content
// on disk.
//
// Traces to: issue #240 regression lock A.
func TestAgentCreateUpdate_ContentOnly_NoMetadataToolBypass(t *testing.T) {
	deps, home := newTestDepsWithHome(t)

	soulContent := "You are a focused research assistant."
	heartbeatContent := "Check in every morning at 9am."

	createTool := systools.NewAgentCreateTool(deps)
	result := createTool.Execute(context.Background(), map[string]any{
		"name":        "Content Only Bot",
		"description": "A content-only research assistant",
		"soul":        soulContent,
		"heartbeat":   heartbeatContent,
		"model":       "test/model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	if result.IsError {
		t.Fatalf("create failed: %s", result.ForLLM)
	}

	wsPath := filepath.Join(home, "agents", "content-only-bot")

	soulOnDisk, err := os.ReadFile(filepath.Join(wsPath, "SOUL.md"))
	if err != nil {
		t.Fatalf("read SOUL.md: %v", err)
	}
	if string(soulOnDisk) != soulContent {
		t.Errorf("SOUL.md content mismatch:\ngot:  %q\nwant: %q", string(soulOnDisk), soulContent)
	}

	hbOnDisk, err := os.ReadFile(filepath.Join(wsPath, "HEARTBEAT.md"))
	if err != nil {
		t.Fatalf("read HEARTBEAT.md: %v", err)
	}
	if string(hbOnDisk) != heartbeatContent {
		t.Errorf("HEARTBEAT.md content mismatch:\ngot:  %q\nwant: %q", string(hbOnDisk), heartbeatContent)
	}

	// Update soul — verify SOUL.md still content-only (no frontmatter injection).
	newSoul := "You are now an expert in data analysis."
	updateTool := systools.NewAgentUpdateTool(deps)
	updateResult := updateTool.Execute(context.Background(), map[string]any{
		"id":   "content-only-bot",
		"soul": newSoul,
	})
	if updateResult.IsError {
		t.Fatalf("update failed: %s", updateResult.ForLLM)
	}

	updatedSoul, err := os.ReadFile(filepath.Join(wsPath, "SOUL.md"))
	if err != nil {
		t.Fatalf("re-read SOUL.md after update: %v", err)
	}
	if string(updatedSoul) != newSoul {
		t.Errorf("SOUL.md after update:\ngot:  %q\nwant: %q", string(updatedSoul), newSoul)
	}

	// Verify no unexpected files exist in workspace root.
	entries, err := os.ReadDir(wsPath)
	if err != nil {
		t.Fatalf("readdir workspace: %v", err)
	}
	for _, e := range entries {
		switch e.Name() {
		case "SOUL.md", "HEARTBEAT.md", "sessions", "memory", "skills":
			// expected
		default:
			t.Errorf("unexpected entry in agent workspace after create/update: %q", e.Name())
		}
	}
}

// TestAgentMetadataTools_RoundTrip verifies system.agent.read_metadata's read
// semantics. Its former write-side companion, agent.write_metadata, was
// retired (tool-manifest-tier-redesign review F6): it was a redundant,
// unguarded second door onto files update_agent already writes through a
// properly-guarded path (update_agent refuses locked core agents;
// write_agent_metadata had no such check). Content is seeded directly on
// disk below — what the write tool used to do in this test — rather than
// through a tool call. The former AGENT.md frontmatter-validation subtests
// (malformed/valid/no-frontmatter) tested write-side behavior that no longer
// exists anywhere in this package and were removed along with the tool.
//
// Scenario A: seed heartbeat content on disk, read it back → exact match.
// Scenario B: read a non-existent file → NOT_FOUND error.
// Scenario C: an unknown file key is rejected.
//
// Traces to: issue #240 regression lock B; tool-manifest-tier-redesign review F6.
func TestAgentMetadataTools_RoundTrip(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	agentID := "test-roundtrip-agent"

	wsPath := filepath.Join(home, "agents", agentID)
	if err := os.MkdirAll(wsPath, 0o700); err != nil {
		t.Fatalf("setup workspace: %v", err)
	}

	readTool := systools.NewAgentReadMetadataTool(deps)

	t.Run("heartbeat_roundtrip", func(t *testing.T) {
		beatContent := "Remind the team about the standup at 10am."

		if err := os.WriteFile(filepath.Join(wsPath, "HEARTBEAT.md"), []byte(beatContent), 0o644); err != nil {
			t.Fatalf("seed HEARTBEAT.md: %v", err)
		}

		readResult := readTool.Execute(context.Background(), map[string]any{
			"file":     "heartbeat",
			"agent_id": agentID,
		})
		if readResult.IsError {
			t.Fatalf("read_metadata(heartbeat) failed: %s", readResult.ForLLM)
		}

		var body struct {
			Content string `json:"content"`
		}
		if err := json.Unmarshal([]byte(readResult.ForLLM), &body); err != nil {
			t.Fatalf("parse read_metadata result: %v", err)
		}
		if body.Content != beatContent {
			t.Errorf("read_metadata returned %q, want %q", body.Content, beatContent)
		}
	})

	t.Run("read_nonexistent_returns_not_found", func(t *testing.T) {
		readResult := readTool.Execute(context.Background(), map[string]any{
			"file":     "memory",
			"agent_id": agentID,
		})
		if !readResult.IsError {
			t.Fatalf("reading nonexistent MEMORY.md should fail, got success: %s", readResult.ForLLM)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(readResult.ForLLM), &m); err != nil {
			t.Fatalf("result not JSON: %v", err)
		}
		errObj, _ := m["error"].(map[string]any)
		if errObj["code"] != "NOT_FOUND" {
			t.Errorf("expected NOT_FOUND error code, got %v", errObj["code"])
		}
	})

	t.Run("invalid_file_key_rejected", func(t *testing.T) {
		result := readTool.Execute(context.Background(), map[string]any{
			"file":     "totally-wrong",
			"agent_id": agentID,
		})
		if !result.IsError {
			t.Fatal("invalid file key should be rejected")
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(result.ForLLM), &m); err != nil {
			t.Fatalf("result not JSON: %v", err)
		}
		errObj, _ := m["error"].(map[string]any)
		if errObj["code"] != "INVALID_INPUT" {
			t.Errorf("expected INVALID_INPUT, got %v", errObj["code"])
		}
	})
}

// TestBash_NewCustomAgentDeniedByDefault proves FR-B12 (bash-tool-spec.md,
// 7-reviewer gate CRIT-001 / BDD "New custom agent is denied bash by default"):
// a freshly created custom agent — created via the REAL system.agent.create
// tool path (AgentCreateTool.Execute), with no explicit bash policy entry
// supplied by the caller — resolves the tool to "deny" by default.
func TestBash_NewCustomAgentDeniedByDefault(t *testing.T) {
	deps, _ := newTestDeps()

	result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Research Bot",
		"description": "A research assistant",
		"soul":        "You are a research bot.",
		"model":       "test/model",
		"color":       "#22C55E",
		"icon":        "robot",
		// Deliberately no bash policy override — proving the DEFAULT seed,
		// not a caller-supplied one.
	})
	if result.IsError {
		t.Fatalf("expected success, got error: %s", result.ForLLM)
	}
	// ADR-054: read back the persisted entity record via the agent store, not
	// cfg.Agents.List — create_agent no longer touches the in-memory config's
	// roster at all.
	newAgent, err := agentstore.New(deps.Home).Get("research-bot")
	if err != nil {
		t.Fatalf("expected agent entity record to exist: %v", err)
	}

	// Sanity: the seed actually landed in the persisted policy map.
	if got := newAgent.Tools.Builtin.Policies["bash"]; got != config.ToolPolicyDeny {
		t.Fatalf(`expected seeded Tools.Builtin.Policies["bash"] = %q, got %q`, config.ToolPolicyDeny, got)
	}

	// Resolve through the single authoritative primitive
	// (pkg/tools/compositor.go's EffectiveToolPolicy), built the same way
	// pkg/agent/instance.go's agentToolsCfgToPolicy converts AgentBuiltinToolsCfg
	// for a non-god-mode agent — matching the calling convention in
	// pkg/tools/effective_tool_policy_test.go.
	policies := make(map[string]config.ToolPolicy, len(newAgent.Tools.Builtin.Policies))
	for k, v := range newAgent.Tools.Builtin.Policies {
		policies[k] = v
	}
	polCfg := &tools.ToolPolicyCfg{
		Policies: policies,
	}

	// A fresh agent created via a display name ("Research Bot" -> "research-bot")
	// never matches a core-agent ID, so ResolveType(nil) correctly resolves to
	// AgentTypeCustom without needing the coreagent package's isCoreAgent (which
	// would create an import cycle from here).
	agentType := string(newAgent.ResolveType(nil))
	if agentType != string(config.AgentTypeCustom) {
		t.Fatalf("test setup invariant broken: expected agentType %q, got %q", config.AgentTypeCustom, agentType)
	}

	got := tools.EffectiveToolPolicy(polCfg, tools.ScopeCore, agentType, "bash")
	if got != "deny" {
		t.Fatalf("EffectiveToolPolicy(bash, ScopeCore, %q) = %q, want %q", agentType, got, "deny")
	}
}

// TestBashScopeCore_UnlistedResolvesToDeny_FailClosedBaseline replaces the
// former "...ResolvesToAllow..." regression baseline. That test hand-built a
// ToolPolicyCfg with DefaultPolicy: allow (no "bash" entry) to prove the
// FR-B12 explicit bash:deny seed was "load-bearing" against a fail-open
// default. DefaultPolicy/GlobalDefaultPolicy were removed project-wide
// (CLAUDE.md hard constraint 6): the compositor itself now fails closed to
// "deny" when a tool has no entry on either side (pkg/tools/compositor.go),
// so an unlisted tool can no longer resolve to "allow" at all — bash's
// explicit seed entry is now redundant with (not load-bearing against) the
// systemic fail-closed default, which is the stronger guarantee. This test
// pins that an unlisted ScopeCore tool with no explicit seed and no global
// coverage resolves to "deny", not "allow".
func TestBashScopeCore_UnlistedResolvesToDeny_FailClosedBaseline(t *testing.T) {
	polCfg := &tools.ToolPolicyCfg{
		Policies: map[string]config.ToolPolicy{
			// Deliberately NO "bash" entry — proving the fail-closed default,
			// not any explicit seed, is what denies it here.
		},
	}

	got := tools.EffectiveToolPolicy(polCfg, tools.ScopeCore, string(config.AgentTypeCustom), "bash")
	if got != "deny" {
		t.Fatalf("fail-closed baseline broken: expected an unlisted ScopeCore tool with no explicit "+
			"seed and no global coverage to resolve to %q, got %q", "deny", got)
	}
}

// TestCreateAgent_NoGlobalAutoAdd_JoinsContextWorkspace (ADR-046 P1,
// FR-007/008, US-3 AS-2/AS-3) proves create_agent's creation-in-context join:
// agents are metadata — creating one must NEVER auto-add it to any global
// roster. When the tool runs inside a workspace's turn context
// (tools.WithWorkspaceID on ctx), the new agent joins THAT workspace's
// core_team only — every other workspace is left untouched. With no
// workspace context, the new agent is metadata-only: a member of no team at
// all, unable to execute until an operator adds it to a workspace's Team tab.
func TestCreateAgent_NoGlobalAutoAdd_JoinsContextWorkspace(t *testing.T) {
	writeWorkspaceJSON := func(t *testing.T, home, id string) string {
		t.Helper()
		wsDir := filepath.Join(home, "workspaces")
		if err := os.MkdirAll(wsDir, 0o755); err != nil {
			t.Fatalf("mkdir workspaces: %v", err)
		}
		path := filepath.Join(wsDir, id+".json")
		body := fmt.Sprintf(`{"id":%q,"name":"ws","status":"active","core_team":[]}`, id)
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write workspace %s: %v", id, err)
		}
		return path
	}
	readCoreTeam := func(t *testing.T, path string) []any {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read workspace: %v", err)
		}
		var ws map[string]any
		if err := json.Unmarshal(data, &ws); err != nil {
			t.Fatalf("unmarshal workspace: %v", err)
		}
		team, _ := ws["core_team"].([]any)
		return team
	}

	t.Run("with workspace context: joins that workspace only", func(t *testing.T) {
		deps, home := newTestDepsWithHome(t)
		ctxWsPath := writeWorkspaceJSON(t, home, "ctx-ws")
		otherWsPath := writeWorkspaceJSON(t, home, "other-ws")

		ctx := tools.WithWorkspaceID(context.Background(), "ctx-ws")
		result := systools.NewAgentCreateTool(deps).Execute(ctx, map[string]any{
			"name":        "Ctx Bot",
			"description": "Created in a workspace context",
			"soul":        "You help.",
			"model":       "test/model",
			"color":       "#22C55E",
			"icon":        "robot",
		})
		if result.IsError {
			t.Fatalf("create failed: %s", result.ForLLM)
		}
		body := parseSuccess(t, result.ForLLM)
		agentID, _ := body["id"].(string)
		if agentID == "" {
			t.Fatal("result missing id")
		}

		ctxTeam := readCoreTeam(t, ctxWsPath)
		found := false
		for _, m := range ctxTeam {
			if s, ok := m.(string); ok && s == agentID {
				found = true
			}
		}
		if !found {
			t.Errorf("new agent %q must be added to the context workspace's core_team, got %v", agentID, ctxTeam)
		}

		otherTeam := readCoreTeam(t, otherWsPath)
		if len(otherTeam) != 0 {
			t.Errorf("an unrelated workspace's core_team must remain untouched, got %v", otherTeam)
		}
	})

	t.Run("without workspace context: metadata-only, joins nothing", func(t *testing.T) {
		deps, home := newTestDepsWithHome(t)
		someWsPath := writeWorkspaceJSON(t, home, "some-ws")

		result := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
			"name":        "Metadata Bot",
			"description": "Created with no workspace context",
			"soul":        "You help.",
			"model":       "test/model",
			"color":       "#22C55E",
			"icon":        "robot",
		})
		if result.IsError {
			t.Fatalf("create failed: %s", result.ForLLM)
		}

		someTeam := readCoreTeam(t, someWsPath)
		if len(someTeam) != 0 {
			t.Errorf("no workspace must gain the new agent when create_agent runs with no workspace "+
				"context, got %v", someTeam)
		}
	})
}

// reloadTestProvider is a minimal providers.LLMProvider stub for
// TestAgentDelete_ImmediatelyUnroutableAndUnlisted_NoRestart — it is never
// actually invoked (the test never runs a real LLM turn); it only needs to
// satisfy agent.NewAgentLoop's constructor signature.
type reloadTestProvider struct{}

func (p *reloadTestProvider) Chat(
	_ context.Context,
	_ []providers.Message,
	_ []providers.ToolDefinition,
	_ string,
	_ map[string]any,
) (*providers.LLMResponse, error) {
	return &providers.LLMResponse{Content: "mock", FinishReason: "stop"}, nil
}

func (p *reloadTestProvider) GetDefaultModel() string { return "mock-model" }

// TestAgentDelete_ImmediatelyUnroutableAndUnlisted_NoRestart is the DoD proof
// for the fix to system.agent.delete/update: both used to write ONLY the
// entity file (agentstore.Store.Delete/Update) — neither mutating the live
// in-memory config nor calling t.deps.ReloadFunc(), unlike AgentCreateTool,
// which already did. A deleted agent therefore stayed live (still routable
// AND still listed) until the process restarted.
//
// This test wires a REAL *agent.AgentLoop's hot-reload path
// (ReloadProviderAndConfig) behind deps.ReloadFunc, mirroring exactly what
// pkg/gateway's own reloadTrigger does in production (re-derive
// cfg.Agents.List/SkippedAgentIDs from the entity store via
// agentstore.Store.List — what populateAgentsListFromEntityStore does —
// then hand the fresh config to ReloadProviderAndConfig to rebuild the
// registry). It proves that calling delete_agent alone, with NO restart and
// NO other call, makes the agent disappear from BOTH the live registry
// (GetAgent/ListAgentIDs) and the channel-binding routing cascade.
func TestAgentDelete_ImmediatelyUnroutableAndUnlisted_NoRestart(t *testing.T) {
	home := t.TempDir()

	cfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:              filepath.Join(home, "workspace"),
				DefaultModel:      config.DefaultModel{Model: "test-model"},
				MaxTokens:         8192,
				MaxToolIterations: 10,
			},
		},
	}

	provider := &reloadTestProvider{}
	al, err := agent.NewAgentLoop(cfg, bus.NewMessageBus(), provider)
	if err != nil {
		t.Fatalf("NewAgentLoop: %v", err)
	}

	const agentID = "reload-test-agent"
	// Bind the "telegram" channel wildcard to the agent BEFORE it exists —
	// ordinary config (an operator can configure a binding for an agent that
	// gets created later, or that existed in a previous session).
	cfg.Bindings = []config.AgentBinding{
		{AgentID: agentID, Match: config.BindingMatch{Channel: "telegram", AccountID: "*"}},
	}

	// reloadFunc mirrors pkg/gateway's real reloadTrigger: re-derive
	// cfg.Agents.List/SkippedAgentIDs from the entity store (exactly what
	// populateAgentsListFromEntityStore does), then hand the fresh config to
	// ReloadProviderAndConfig so the registry is rebuilt from it.
	reloadFunc := func() error {
		agents, skipped, listErr := agentstore.New(home).List()
		if listErr != nil {
			return fmt.Errorf("list agent entity records: %w", listErr)
		}
		newCfg := *al.GetConfig()
		newCfg.Agents.List = agents
		newCfg.SkippedAgentIDs = skipped
		return al.ReloadProviderAndConfig(context.Background(), provider, &newCfg)
	}

	deps := &systools.Deps{
		Home:   home,
		GetCfg: al.GetConfig,
		MutateConfig: func(fn func(*config.Config) error) error {
			return fn(al.GetConfig())
		},
		SaveConfigLocked: func(*config.Config) error { return nil },
		ReloadFunc:       reloadFunc,
	}

	createResult := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Reload Test Agent",
		"description": "proves delete_agent hot-reloads without a restart",
		"soul":        "You are a test agent.",
		"model":       "test-model",
		"color":       "#22C55E",
		"icon":        "robot",
	})
	if createResult.IsError {
		t.Fatalf("create_agent failed: %s", createResult.ForLLM)
	}
	created := parseSuccess(t, createResult.ForLLM)
	if got, _ := created["id"].(string); got != agentID {
		t.Fatalf("create_agent id = %q, want %q (slug mismatch — fix the test's expected ID)", got, agentID)
	}

	// A SECOND, surviving agent — deliberately kept in the roster so that
	// after agentID is deleted, cfg.Agents.List is non-empty. Without this,
	// pickAgentID's own len(agents)==0 branch (a pre-existing, unrelated
	// quirk: with a completely empty roster it trusts a binding's raw agent
	// ID as-is, since there is nothing to validate against) would make the
	// post-delete route resolve to the literal string "reload-test-agent"
	// regardless of whether the entity/registry were actually refreshed —
	// masking the very hot-reload behavior this test exists to prove.
	keeperResult := systools.NewAgentCreateTool(deps).Execute(context.Background(), map[string]any{
		"name":        "Keeper Agent",
		"description": "stays in the roster after the other agent is deleted",
		"soul":        "You persist.",
		"model":       "test-model",
		"color":       "#3366FF",
		"icon":        "robot",
	})
	if keeperResult.IsError {
		t.Fatalf("create_agent (keeper) failed: %s", keeperResult.ForLLM)
	}

	// Precondition: create_agent's OWN ReloadFunc call already makes the
	// agent immediately live — registered AND routable — with no restart.
	reg := al.GetRegistry()
	if _, ok := reg.GetAgent(agentID); !ok {
		t.Fatalf("test setup: expected %q to be registered immediately after create", agentID)
	}
	resolver := routing.NewRouteResolver(al.GetConfig())
	route := resolver.ResolveRoute(routing.RouteInput{Channel: "telegram", AccountID: "acct-1"})
	if route.AgentID != agentID {
		t.Fatalf("test setup: expected the telegram binding to route to %q, got %q (matched_by=%s)",
			agentID, route.AgentID, route.MatchedBy)
	}

	// Delete the agent — the fix under test.
	deleteResult := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      agentID,
		"confirm": true,
	})
	if deleteResult.IsError {
		t.Fatalf("delete_agent failed: %s", deleteResult.ForLLM)
	}
	parseSuccess(t, deleteResult.ForLLM)

	// The entity record is gone on disk (pre-existing behavior, unaffected
	// by this fix).
	if _, err := agentstore.New(home).Get(agentID); !errors.Is(err, entity.ErrNotFound) {
		t.Fatalf("expected the entity record to be deleted, Get error = %v", err)
	}

	// UNLISTED, with NO restart: the live registry must no longer contain it.
	regAfter := al.GetRegistry()
	if _, ok := regAfter.GetAgent(agentID); ok {
		t.Error("BUG: deleted agent is still present in the live registry — delete_agent did not hot-reload")
	}
	for _, id := range regAfter.ListAgentIDs() {
		if id == agentID {
			t.Error("BUG: deleted agent still appears in ListAgentIDs() — delete_agent did not hot-reload")
		}
	}

	// UNROUTABLE, with NO restart: the same channel binding must no longer
	// resolve to the deleted agent — it falls back to the default tier
	// instead (pickAgentID's non-existent-agent branch).
	resolverAfter := routing.NewRouteResolver(al.GetConfig())
	routeAfter := resolverAfter.ResolveRoute(routing.RouteInput{Channel: "telegram", AccountID: "acct-1"})
	if routeAfter.AgentID == agentID {
		t.Error("BUG: the telegram binding still routes to the deleted agent — delete_agent did not hot-reload")
	}
}
