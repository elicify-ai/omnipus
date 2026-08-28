// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for Bug 1 (AgentsConfig.List must never round-trip through
// config.json, see its doc comment on config.go) and Bug 2's sibling fix in
// this package (Bug 1b): legacy_agents_list.go's stripLegacyAgentsList must
// log an accurate, split WARN — core agent IDs (auto-reseeded, no operator
// action) vs custom agent IDs (real data loss, operator must recreate) —
// instead of one blanket "must be recreated" message that is false for core
// agents.

package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestSaveConfig_NeverPersistsAgentsList is the primary DoD proof for Bug 1:
// a SaveConfig round-trip must not persist agents.list to config.json even
// when cfg.Agents.List is populated in memory (e.g. because the roster
// bridge already repopulated it from the entity store, as happens on every
// real boot per ADR-054).
//
// Before the fix (AgentsConfig.List `json:"list,omitempty"`), this exact
// scenario — a config write triggered for an unrelated reason (the bug
// report's example: `system.config.set gateway.log_level`) while the live
// roster sits in cfg.Agents.List — re-serialized the entire agent roster
// back into config.json, degrading ADR-054's "config.json no longer carries
// the roster" from a structural guarantee into a self-healing loop.
func TestSaveConfig_NeverPersistsAgentsList(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	cfg := DefaultConfig()
	now := time.Now()
	cfg.Agents.List = []AgentConfig{
		{ID: "mia", Name: "Mia", Locked: true, CreatedAt: &now},
		{ID: "widget-bot", Name: "Widget Bot", Home: "~/.omnipus/workspace/widget-bot"},
	}

	if err := SaveConfig(path, cfg); err != nil {
		t.Fatalf("SaveConfig failed: %v", err)
	}

	// Inspect the raw bytes written to disk: no "list" key under "agents",
	// no matter how populated cfg.Agents.List was at save time.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	var onDisk map[string]any
	if unmarshalErr := json.Unmarshal(raw, &onDisk); unmarshalErr != nil {
		t.Fatalf("unmarshal written config: %v", unmarshalErr)
	}
	agentsSection, ok := onDisk["agents"].(map[string]any)
	if !ok {
		t.Fatalf("config.json must still have an agents section (defaults), got: %s", raw)
	}
	if _, hasList := agentsSection["list"]; hasList {
		t.Fatalf("config.json must NOT contain agents.list, got: %s", raw)
	}
	// Sanity: the agent IDs must not appear ANYWHERE in the written bytes —
	// guards against a future regression that serializes the roster under a
	// different key name.
	rawStr := string(raw)
	for _, needle := range []string{`"widget-bot"`, `"Widget Bot"`} {
		if containsSubstring(rawStr, needle) {
			t.Errorf("config.json must not contain agent data %q, got: %s", needle, rawStr)
		}
	}

	// Reload through the real load path: the roster must come back empty
	// (it is the entity store's job, not config.json's, to repopulate it).
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(loaded.Agents.List) != 0 {
		t.Fatalf("loaded.Agents.List should be empty after a SaveConfig/LoadConfig round-trip, got %d: %+v",
			len(loaded.Agents.List), loaded.Agents.List)
	}
}

// TestConfigClone_DeepCopiesAgentsList proves Config.Clone() still performs a
// fully independent deep copy of Agents.List even though it is json:"-" on
// AgentsConfig — Clone's own doc comment documents that it round-trips List
// via its own separate JSON encode/decode specifically to preserve this.
func TestConfigClone_DeepCopiesAgentsList(t *testing.T) {
	orig := DefaultConfig()
	orig.Agents.List = []AgentConfig{
		{ID: "agent-1", Name: "Original", Model: &AgentModelConfig{Primary: "gpt-4o"}},
	}

	clone, err := orig.Clone()
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}
	if len(clone.Agents.List) != 1 {
		t.Fatalf("clone.Agents.List len = %d, want 1", len(clone.Agents.List))
	}
	if clone.Agents.List[0].ID != "agent-1" || clone.Agents.List[0].Name != "Original" {
		t.Fatalf("clone.Agents.List[0] = %+v", clone.Agents.List[0])
	}

	// Mutating the original's slice/struct/nested-pointer fields after
	// cloning must not affect the clone.
	orig.Agents.List = append(orig.Agents.List, AgentConfig{ID: "agent-2"})
	orig.Agents.List[0].Name = "Changed"
	orig.Agents.List[0].Model.Primary = "changed-model"

	if len(clone.Agents.List) != 1 {
		t.Errorf("clone.Agents.List length changed after mutating original: %d", len(clone.Agents.List))
	}
	if clone.Agents.List[0].Name != "Original" {
		t.Errorf("clone.Agents.List[0].Name = %q after mutating original; want Original", clone.Agents.List[0].Name)
	}
	if clone.Agents.List[0].Model.Primary != "gpt-4o" {
		t.Errorf("clone.Agents.List[0].Model.Primary = %q after mutating original's nested pointer; want gpt-4o",
			clone.Agents.List[0].Model.Primary)
	}
}

// TestConfigClone_EmptyAgentsList proves Clone handles a nil/empty roster
// without panicking or fabricating entries.
func TestConfigClone_EmptyAgentsList(t *testing.T) {
	orig := DefaultConfig()
	clone, err := orig.Clone()
	if err != nil {
		t.Fatalf("Clone failed: %v", err)
	}
	if len(clone.Agents.List) != 0 {
		t.Fatalf("clone.Agents.List should be empty, got %+v", clone.Agents.List)
	}
}

// TestStripAgentsListOnDisk_ClassifiesEverySeededID pins the whole seeded
// roster, not a sample of it. coreAgentIDs is a hand-maintained mirror of
// pkg/coreagent's roster (the import cycle makes a derived list impossible),
// so the realistic failure is that a NEW seeded agent is added to coreagent
// and nobody updates the mirror — which is exactly what happened to
// plansupervisor: a legacy config.json carrying it was reported to the
// operator with the alarming "real, operator-authored data loss" WARN for an
// agent that SeedConfig recreates moments later on the same boot.
//
// This asserts the property (every seeded ID classifies as core) rather than
// the map's contents, so it fails on the omission rather than on a rewrite.
func TestStripAgentsListOnDisk_ClassifiesEverySeededID(t *testing.T) {
	// Mirrors pkg/coreagent's seeded roster: the four base agents plus the
	// System Agents. Kept literal for the same import-cycle reason as
	// coreAgentIDs itself.
	seeded := []string{"mia", "jim", "ava", "ray", "judge", "plansupervisor"}

	for _, id := range seeded {
		t.Run(id, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			raw := `{"version":1,"agents":{"list":[{"id":"` + id + `","name":"X"}]}}`
			if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}

			coreIDs, customIDs, written, err := stripAgentsListOnDisk(path)
			if err != nil {
				t.Fatalf("stripAgentsListOnDisk failed: %v", err)
			}
			if written == nil {
				t.Fatal("expected the legacy list to be stripped")
			}
			if len(customIDs) != 0 {
				t.Errorf("seeded agent %q was reported as a CUSTOM id (%v) — the operator is "+
					"told this is unrecoverable data loss, but coreagent.SeedConfig recreates it "+
					"on this same boot. Add %q to coreAgentIDs.", id, customIDs, id)
			}
			if len(coreIDs) != 1 || coreIDs[0] != id {
				t.Errorf("coreIDs = %v, want [%s]", coreIDs, id)
			}
		})
	}
}

// TestStripAgentsListOnDisk_SplitsCoreAndCustomIDs is the DoD proof for Bug
// 1b: core agent IDs must be reported separately from custom agent IDs so the
// caller can log accurate, non-misleading guidance (core IDs self-heal with
// zero operator action; custom IDs are real, unrecoverable-by-migration data
// loss).
func TestStripAgentsListOnDisk_SplitsCoreAndCustomIDs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {
			"defaults": {"workspace": "/tmp/ws"},
			"list": [
				{"id": "mia", "name": "Mia"},
				{"id": "jim", "name": "Jim"},
				{"id": "widget-bot", "name": "Widget Bot"}
			]
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	coreIDs, customIDs, written, err := stripAgentsListOnDisk(path)
	if err != nil {
		t.Fatalf("stripAgentsListOnDisk failed: %v", err)
	}
	if written == nil {
		t.Fatal("expected a write to occur (written should be non-nil)")
	}
	if len(coreIDs) != 2 || !containsString(coreIDs, "mia") || !containsString(coreIDs, "jim") {
		t.Errorf("coreIDs = %v, want [mia jim] (order-independent)", coreIDs)
	}
	if len(customIDs) != 1 || customIDs[0] != "widget-bot" {
		t.Errorf("customIDs = %v, want [widget-bot]", customIDs)
	}

	// agents.defaults must survive untouched; list must be gone.
	var m map[string]any
	if unmarshalErr := json.Unmarshal(written, &m); unmarshalErr != nil {
		t.Fatalf("unmarshal written bytes: %v", unmarshalErr)
	}
	agents, ok := m["agents"].(map[string]any)
	if !ok {
		t.Fatalf("m[\"agents\"] has unexpected type %T, want map[string]any", m["agents"])
	}
	if _, hasList := agents["list"]; hasList {
		t.Error("written bytes must not contain agents.list")
	}
	defaults, ok := agents["defaults"].(map[string]any)
	if !ok || defaults["workspace"] != "/tmp/ws" {
		t.Errorf("agents.defaults must survive untouched, got: %+v", agents["defaults"])
	}

	// Re-reading the file from disk must reflect the same self-heal.
	onDiskRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read fixture: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(onDiskRaw, &onDisk); err != nil {
		t.Fatalf("unmarshal on-disk bytes: %v", err)
	}
	if _, hasList := onDisk["agents"].(map[string]any)["list"]; hasList {
		t.Error("on-disk file must have agents.list stripped")
	}
}

// TestStripAgentsListOnDisk_NoAgentsSection_NoOp and
// TestStripAgentsListOnDisk_NoListKey_NoOp prove the no-op contract: a file
// with no agents.list content at all must not be rewritten.
func TestStripAgentsListOnDisk_NoAgentsSection_NoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"version": 1}`), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	coreIDs, customIDs, written, err := stripAgentsListOnDisk(path)
	if err != nil {
		t.Fatalf("stripAgentsListOnDisk failed: %v", err)
	}
	if coreIDs != nil || customIDs != nil || written != nil {
		t.Fatalf("expected a true no-op, got coreIDs=%v customIDs=%v written=%v", coreIDs, customIDs, written)
	}
}

func TestStripAgentsListOnDisk_NoListKey_NoOp(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{"version": 1, "agents": {"defaults": {"workspace": "/tmp/ws"}}}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	coreIDs, customIDs, written, err := stripAgentsListOnDisk(path)
	if err != nil {
		t.Fatalf("stripAgentsListOnDisk failed: %v", err)
	}
	if coreIDs != nil || customIDs != nil || written != nil {
		t.Fatalf("expected a true no-op (already clean), got coreIDs=%v customIDs=%v written=%v", coreIDs, customIDs, written)
	}
}

// TestLoadConfig_LegacyAgentsList_SelfHealsAndNeverPopulatesInMemory is a
// full LoadConfig integration test: a legacy config.json carrying
// agents.list must self-heal on disk (list key removed, everything else
// untouched) and must never populate cfg.Agents.List in memory, regardless
// of whether the entries are core or custom agent IDs.
func TestLoadConfig_LegacyAgentsList_SelfHealsAndNeverPopulatesInMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {
			"defaults": {"workspace": "` + dir + `", "default_model": {"provider": "", "model": "test-model"}, "max_tokens": 4096},
			"list": [
				{"id": "judge", "name": "Judge"},
				{"id": "old-custom-agent", "name": "Old Custom Agent"}
			]
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}
	if len(cfg.Agents.List) != 0 {
		t.Fatalf("cfg.Agents.List must be empty after load, got %d: %+v", len(cfg.Agents.List), cfg.Agents.List)
	}
	if cfg.Agents.Defaults.DefaultModel.Model != "test-model" {
		t.Errorf("Agents.Defaults.DefaultModel.Model = %q, want test-model", cfg.Agents.Defaults.DefaultModel.Model)
	}

	onDiskRaw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("re-read config: %v", err)
	}
	var onDisk map[string]any
	if err := json.Unmarshal(onDiskRaw, &onDisk); err != nil {
		t.Fatalf("unmarshal on-disk config: %v", err)
	}
	agents, ok := onDisk["agents"].(map[string]any)
	if !ok {
		t.Fatalf("agents section must survive on disk, got: %s", onDiskRaw)
	}
	if _, hasList := agents["list"]; hasList {
		t.Errorf("on-disk config.json must have agents.list self-healed away, got: %s", onDiskRaw)
	}
	if agents["defaults"] == nil {
		t.Error("agents.defaults must survive the self-heal on disk")
	}
}

// TestLoadConfigWithStoreAndSelfHealHook_AgentsListStrip_InvokesHook proves
// the on-disk agents.list strip reports through the SelfHealWriteHook, the
// same mechanism migrateCLITokenOutOfUsers uses — so pkg/gateway's
// write-dedup registry can recognize this self-heal as app-initiated rather
// than a genuine external edit.
func TestLoadConfigWithStoreAndSelfHealHook_AgentsListStrip_InvokesHook(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	raw := `{
		"version": 1,
		"agents": {
			"defaults": {"workspace": "` + dir + `"},
			"list": [{"id": "custom-agent", "name": "Custom Agent"}]
		}
	}`
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	var hookBytes []byte
	hookCalls := 0
	hook := func(written []byte) {
		hookCalls++
		hookBytes = written
	}

	if _, err := LoadConfigWithStoreAndSelfHealHook(path, nil, hook); err != nil {
		t.Fatalf("LoadConfigWithStoreAndSelfHealHook failed: %v", err)
	}
	if hookCalls != 1 {
		t.Fatalf("expected self-heal hook to fire exactly once, fired %d times", hookCalls)
	}
	var m map[string]any
	if err := json.Unmarshal(hookBytes, &m); err != nil {
		t.Fatalf("unmarshal hook bytes: %v", err)
	}
	if _, hasList := m["agents"].(map[string]any)["list"]; hasList {
		t.Error("bytes reported to the self-heal hook must have agents.list stripped")
	}
}

// --- small local helpers (avoid pulling in a slices/testify dependency for
// a couple of trivial membership checks) ---

func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}

func containsSubstring(haystack, needle string) bool {
	return len(needle) > 0 && len(haystack) >= len(needle) && stringsIndex(haystack, needle) >= 0
}

func stringsIndex(haystack, needle string) int {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return i
		}
	}
	return -1
}
