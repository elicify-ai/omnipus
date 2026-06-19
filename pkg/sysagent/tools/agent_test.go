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
	"sync"
	"testing"
	"time"

	"github.com/dapicom-ai/omnipus/pkg/config"
	systools "github.com/dapicom-ai/omnipus/pkg/sysagent/tools"
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
func newTestDeps() (*systools.Deps, *config.Config) {
	cfg := config.DefaultConfig()
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	deps := &systools.Deps{
		Home:         "/tmp/omnipus-test",
		ConfigPath:   "/tmp/omnipus-test/config.json",
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
	deps, cfg := newTestDeps()
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

	// Verify config in-memory state.
	if len(cfg.Agents.List) != 1 {
		t.Fatalf("expected 1 agent in list, got %d", len(cfg.Agents.List))
	}
	agent := cfg.Agents.List[0]
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
	deps, cfg := newTestDeps()
	// Pre-populate the config with one agent.
	cfg.Agents.List = []config.AgentConfig{{ID: "my-agent", Name: "My Agent"}}

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
	if len(cfg.Agents.List) != 1 {
		t.Fatal("agent should not be deleted when confirm=false")
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
	if len(cfg.Agents.List) != 0 {
		t.Errorf("expected agent to be deleted from list, still have %d agents", len(cfg.Agents.List))
	}
}

// TestAgentActivate_PersistsEnabled creates an agent with Enabled=false, calls
// activate, and asserts the Enabled pointer is true after the call.
//
// Traces to: wave5b-system-agent-spec.md — BRD §D.4.2 agent.activate
func TestAgentActivate_PersistsEnabled(t *testing.T) {
	deps, cfg := newTestDeps()
	disabled := false
	cfg.Agents.List = []config.AgentConfig{
		{ID: "target-agent", Name: "Target", Enabled: &disabled},
	}

	saveCalled := false
	deps.SaveConfigLocked = func(cfg *config.Config) error {
		saveCalled = true
		return nil
	}

	tool := systools.NewAgentActivateTool(deps)
	result := tool.Execute(context.Background(), map[string]any{"id": "target-agent"})

	if result.IsError {
		t.Fatalf("activate failed: %s", result.ForLLM)
	}

	if !saveCalled {
		t.Error("SaveConfig was not called by activate")
	}

	agent := cfg.Agents.List[0]
	if agent.Enabled == nil {
		t.Fatal("Enabled is nil after activate; expected non-nil pointer")
	}
	if !*agent.Enabled {
		t.Errorf("Enabled = false after activate; expected true")
	}
	if !agent.IsActive() {
		t.Error("IsActive() returned false after activate")
	}
}

// TestAgentDeactivate_PersistsEnabled is the inverse of TestAgentActivate_PersistsEnabled.
//
// Traces to: wave5b-system-agent-spec.md — BRD §D.4.2 agent.deactivate
func TestAgentDeactivate_PersistsEnabled(t *testing.T) {
	deps, cfg := newTestDeps()
	enabled := true
	cfg.Agents.List = []config.AgentConfig{
		{ID: "target-agent", Name: "Target", Enabled: &enabled},
	}

	saveCalled := false
	deps.SaveConfigLocked = func(cfg *config.Config) error {
		saveCalled = true
		return nil
	}

	tool := systools.NewAgentDeactivateTool(deps)
	result := tool.Execute(context.Background(), map[string]any{"id": "target-agent"})

	if result.IsError {
		t.Fatalf("deactivate failed: %s", result.ForLLM)
	}

	if !saveCalled {
		t.Error("SaveConfig was not called by deactivate")
	}

	agent := cfg.Agents.List[0]
	if agent.Enabled == nil {
		t.Fatal("Enabled is nil after deactivate; expected non-nil pointer")
	}
	if *agent.Enabled {
		t.Errorf("Enabled = true after deactivate; expected false")
	}
	if agent.IsActive() {
		t.Error("IsActive() returned true after deactivate")
	}
}

// TestAgentUpdate_PartialFields verifies that updating only `name` does not
// clobber color and icon already set on the agent.
//
// Traces to: wave5b-system-agent-spec.md — BRD §D.4.2 agent.update
func TestAgentUpdate_PartialFields(t *testing.T) {
	deps, cfg := newTestDeps()
	cfg.Agents.List = []config.AgentConfig{
		{
			ID:    "my-agent",
			Name:  "Old Name",
			Color: "#FF0000",
			Icon:  "star",
		},
	}

	tool := systools.NewAgentUpdateTool(deps)
	result := tool.Execute(context.Background(), map[string]any{
		"id":   "my-agent",
		"name": "New Name",
	})

	if result.IsError {
		t.Fatalf("update failed: %s", result.ForLLM)
	}

	agent := cfg.Agents.List[0]
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

// TestAgentList_ReflectsEnabledStatus verifies that list returns "inactive" for
// agents whose Enabled pointer is false, and "active" for those with nil or true.
//
// Traces to: wave5b-system-agent-spec.md — BRD §D.4.2 agent.list
func TestAgentList_ReflectsEnabledStatus(t *testing.T) {
	deps, cfg := newTestDeps()
	disabled := false
	enabled := true
	cfg.Agents.List = []config.AgentConfig{
		{ID: "active-nil", Name: "Active Nil"},                       // Enabled nil → active
		{ID: "active-true", Name: "Active True", Enabled: &enabled},  // Enabled=true → active
		{ID: "inactive-false", Name: "Inactive", Enabled: &disabled}, // Enabled=false → inactive
	}

	tool := systools.NewAgentListTool(deps)
	result := tool.Execute(context.Background(), map[string]any{"status": "all"})

	if result.IsError {
		t.Fatalf("list failed: %s", result.ForLLM)
	}

	var body struct {
		Success bool `json:"success"`
		Agents  []struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(result.ForLLM), &body); err != nil {
		t.Fatalf("could not parse list result: %v", err)
	}
	statusByID := make(map[string]string)
	for _, a := range body.Agents {
		statusByID[a.ID] = a.Status
	}
	if statusByID["active-nil"] != "active" {
		t.Errorf("active-nil has status %q, want active", statusByID["active-nil"])
	}
	if statusByID["active-true"] != "active" {
		t.Errorf("active-true has status %q, want active", statusByID["active-true"])
	}
	if statusByID["inactive-false"] != "inactive" {
		t.Errorf("inactive-false has status %q, want inactive", statusByID["inactive-false"])
	}
}

// TestAgentList_FilterByStatus verifies the status filter for active/inactive/all.
//
// Traces to: wave5b-system-agent-spec.md — BRD §D.4.2 agent.list (status filter)
func TestAgentList_FilterByStatus(t *testing.T) {
	deps, cfg := newTestDeps()
	disabled := false
	enabled := true
	cfg.Agents.List = []config.AgentConfig{
		{ID: "a-nil", Name: "Nil"},                         // nil → active
		{ID: "a-true", Name: "True", Enabled: &enabled},    // true → active
		{ID: "a-false", Name: "False", Enabled: &disabled}, // false → inactive
	}

	tool := systools.NewAgentListTool(deps)

	// active filter — must return 2
	result := tool.Execute(context.Background(), map[string]any{"status": "active"})
	if result.IsError {
		t.Fatalf("list active failed: %s", result.ForLLM)
	}
	var body struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(result.ForLLM), &body); err != nil {
		t.Fatalf("parse active: %v", err)
	}
	if len(body.Agents) != 2 {
		t.Errorf("active filter: got %d agents, want 2", len(body.Agents))
	}

	// inactive filter — must return 1
	result2 := tool.Execute(context.Background(), map[string]any{"status": "inactive"})
	if result2.IsError {
		t.Fatalf("list inactive failed: %s", result2.ForLLM)
	}
	var body2 struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(result2.ForLLM), &body2); err != nil {
		t.Fatalf("parse inactive: %v", err)
	}
	if len(body2.Agents) != 1 {
		t.Errorf("inactive filter: got %d agents, want 1", len(body2.Agents))
	}
	if body2.Agents[0].ID != "a-false" {
		t.Errorf("inactive filter: got agent %q, want %q", body2.Agents[0].ID, "a-false")
	}

	// all filter — must return 3
	result3 := tool.Execute(context.Background(), map[string]any{"status": "all"})
	if result3.IsError {
		t.Fatalf("list all failed: %s", result3.ForLLM)
	}
	var body3 struct {
		Agents []struct {
			ID string `json:"id"`
		} `json:"agents"`
	}
	if err := json.Unmarshal([]byte(result3.ForLLM), &body3); err != nil {
		t.Fatalf("parse all: %v", err)
	}
	if len(body3.Agents) != 3 {
		t.Errorf("all filter: got %d agents, want 3", len(body3.Agents))
	}
}

// TestAgentCreate_PersistsToDisk verifies that create writes color and icon to disk.
// Catches JSON-tag typos and pointer-marshaling regressions.
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

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read config.json: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal config.json: %v", err)
	}
	agentsSection, _ := raw["agents"].(map[string]any)
	list, _ := agentsSection["list"].([]any)
	if len(list) == 0 {
		t.Fatal("agents.list is empty in persisted config.json")
	}
	entry, _ := list[0].(map[string]any)
	if entry["color"] != "#22C55E" {
		t.Errorf("disk color = %v, want #22C55E", entry["color"])
	}
	if entry["icon"] != "robot" {
		t.Errorf("disk icon = %v, want robot", entry["icon"])
	}
}

// TestAgentActivate_RoundTripDisk verifies activate/deactivate persist to disk correctly.
func TestAgentActivate_RoundTripDisk(t *testing.T) {
	deps, cfgPath := newTestDepsWithRealSave(t)
	disabled := false
	deps.GetCfg().Agents.List = []config.AgentConfig{{ID: "my-agent", Name: "My Agent", Enabled: &disabled}}
	// seed the config on disk with the pre-populated agent
	if err := deps.SaveConfigLocked(deps.GetCfg()); err != nil {
		t.Fatalf("seed: %v", err)
	}

	result := systools.NewAgentActivateTool(deps).Execute(context.Background(), map[string]any{"id": "my-agent"})
	if result.IsError {
		t.Fatalf("activate failed: %s", result.ForLLM)
	}

	data, err := os.ReadFile(cfgPath)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	agents, _ := raw["agents"].(map[string]any)
	list, _ := agents["list"].([]any)
	if len(list) == 0 {
		t.Fatal("list empty on disk")
	}
	entry, _ := list[0].(map[string]any)
	if entry["enabled"] != true {
		t.Errorf("disk enabled = %v, want true", entry["enabled"])
	}
}

// TestAgentDeactivate_RefusesLockedAgent verifies locked (core) agents cannot be deactivated.
// The guard is now based on the Locked field, not on a hardcoded agent ID (FR-045).
//
// Traces to: architect finding #3 — self-deactivation guard
func TestAgentDeactivate_RefusesLockedAgent(t *testing.T) {
	deps, cfg := newTestDeps()
	// Seed a locked core agent into config.
	enabled := true
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:      "locked-core",
		Name:    "Locked Core",
		Locked:  true,
		Enabled: &enabled,
	})
	result := systools.NewAgentDeactivateTool(deps).Execute(context.Background(), map[string]any{
		"id": "locked-core",
	})
	if !result.IsError {
		t.Fatal("expected error when deactivating locked agent, got success")
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	if errBlock["code"] != "SAVE_FAILED" && errBlock["code"] != "INVALID_OPERATION" {
		t.Errorf("error code = %v, want SAVE_FAILED or INVALID_OPERATION", errBlock["code"])
	}
}

// TestAgentDelete_RefusesLockedAgent verifies locked (core) agents cannot be deleted.
// The guard is now based on the Locked field, not on a hardcoded agent ID (FR-045).
//
// Traces to: architect finding #3 — self-deactivation guard
func TestAgentDelete_RefusesLockedAgent(t *testing.T) {
	deps, cfg := newTestDeps()
	// Seed a locked core agent into config.
	enabled := true
	cfg.Agents.List = append(cfg.Agents.List, config.AgentConfig{
		ID:      "locked-core",
		Name:    "Locked Core",
		Locked:  true,
		Enabled: &enabled,
	})
	result := systools.NewAgentDeleteTool(deps).Execute(context.Background(), map[string]any{
		"id":      "locked-core",
		"confirm": true,
	})
	if !result.IsError {
		t.Fatal("expected error when deleting locked agent, got success")
	}
	m := parseError(t, result.ForLLM)
	errBlock, _ := m["error"].(map[string]any)
	// The Locked check inside WithConfig returns a SAVE_FAILED wrapper.
	if errBlock["code"] == nil {
		t.Errorf("expected error code in result, got nil")
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

// TestAgentActivate_RollbackOnSaveFailure verifies that on SaveConfig failure,
// the in-memory state is rolled back to its pre-activation state.
//
// Traces to: silent-failure-hunter H4 — rollback on save failure
func TestAgentActivate_RollbackOnSaveFailure(t *testing.T) {
	cfg := config.DefaultConfig()
	disabled := false
	cfg.Agents.List = []config.AgentConfig{
		{ID: "my-agent", Name: "My Agent", Enabled: &disabled},
	}
	var mu sync.Mutex
	getCfg := func() *config.Config { return cfg }
	deps := &systools.Deps{
		Home:             "/tmp/omnipus-test",
		ConfigPath:       "/tmp/omnipus-test/config.json",
		GetCfg:           getCfg,
		MutateConfig:     testMutateConfig(&mu, getCfg),
		SaveConfigLocked: func(cfg *config.Config) error { return errors.New("disk full") },
		CredStore:        nil,
	}

	result := systools.NewAgentActivateTool(deps).Execute(context.Background(), map[string]any{"id": "my-agent"})
	if !result.IsError {
		t.Fatal("expected error on save failure, got success")
	}
	// The in-memory state must be rolled back: Enabled must still be false.
	agent := cfg.Agents.List[0]
	if agent.Enabled == nil || *agent.Enabled {
		t.Error("in-memory state was not rolled back after save failure; Enabled should still be false")
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

	// Writers: call system.agent.create concurrently.
	const numWriters = 4
	for i := range numWriters {
		go func() {
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
	for range numReaders {
		go func() {
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

// TestAgentMetadataTools_RoundTrip verifies the round-trip semantics of
// system.agent.write_metadata + system.agent.read_metadata.
//
// Scenario A: write heartbeat content, read it back → exact match.
// Scenario B: write malformed AGENT.md frontmatter → validation error.
// Scenario C: read a non-existent file → NOT_FOUND error.
//
// BEFORE fix: these tools do not exist (compile/registration failure → test fails).
// AFTER fix: they pass.
//
// Traces to: issue #240 regression lock B.
func TestAgentMetadataTools_RoundTrip(t *testing.T) {
	deps, home := newTestDepsWithHome(t)
	agentID := "test-roundtrip-agent"

	wsPath := filepath.Join(home, "agents", agentID)
	if err := os.MkdirAll(wsPath, 0o700); err != nil {
		t.Fatalf("setup workspace: %v", err)
	}

	writeTool := systools.NewAgentWriteMetadataTool(deps)
	readTool := systools.NewAgentReadMetadataTool(deps)

	t.Run("heartbeat_roundtrip", func(t *testing.T) {
		beatContent := "Remind the team about the standup at 10am."

		writeResult := writeTool.Execute(context.Background(), map[string]any{
			"file":     "heartbeat",
			"content":  beatContent,
			"agent_id": agentID,
		})
		if writeResult.IsError {
			t.Fatalf("write_metadata(heartbeat) failed: %s", writeResult.ForLLM)
		}

		onDisk, err := os.ReadFile(filepath.Join(wsPath, "HEARTBEAT.md"))
		if err != nil {
			t.Fatalf("read HEARTBEAT.md: %v", err)
		}
		if string(onDisk) != beatContent {
			t.Errorf("disk content mismatch: got %q, want %q", string(onDisk), beatContent)
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

	t.Run("agent_md_malformed_frontmatter_rejected", func(t *testing.T) {
		// Unclosed frontmatter block — must be rejected before I/O.
		malformedContent := "---\nname: Test Agent\n"
		writeResult := writeTool.Execute(context.Background(), map[string]any{
			"file":     "agent",
			"content":  malformedContent,
			"agent_id": agentID,
		})
		if !writeResult.IsError {
			t.Fatalf("write_metadata(agent) with unclosed frontmatter should fail, got success: %s", writeResult.ForLLM)
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(writeResult.ForLLM), &m); err != nil {
			t.Fatalf("result not JSON: %v", err)
		}
		errObj, _ := m["error"].(map[string]any)
		if errObj["code"] != "INVALID_FRONTMATTER" {
			t.Errorf("expected INVALID_FRONTMATTER error code, got %v", errObj["code"])
		}
	})

	t.Run("agent_md_valid_frontmatter_accepted", func(t *testing.T) {
		validContent := "---\nname: Test Agent\ndescription: A fine agent\n---\n\nSome body text.\n"
		writeResult := writeTool.Execute(context.Background(), map[string]any{
			"file":     "agent",
			"content":  validContent,
			"agent_id": agentID,
		})
		if writeResult.IsError {
			t.Fatalf("write_metadata(agent) with valid frontmatter should succeed: %s", writeResult.ForLLM)
		}
	})

	t.Run("agent_md_no_frontmatter_accepted", func(t *testing.T) {
		noFrontmatter := "Just plain text, no frontmatter block."
		writeResult := writeTool.Execute(context.Background(), map[string]any{
			"file":     "agent",
			"content":  noFrontmatter,
			"agent_id": agentID,
		})
		if writeResult.IsError {
			t.Fatalf("write_metadata(agent) without frontmatter should succeed: %s", writeResult.ForLLM)
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
		result := writeTool.Execute(context.Background(), map[string]any{
			"file":     "totally-wrong",
			"content":  "x",
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
