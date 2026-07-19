// Omnipus — sub-agent worker tier: config helpers + default repair tests.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// TestIsExternalCLIWorker verifies AgentConfig.IsExternalCLIWorker.
func TestIsExternalCLIWorker(t *testing.T) {
	// native worker — not external CLI.
	native := AgentConfig{
		ID:   "w-native",
		Type: AgentTypeWorker,
		Subagents: &SubagentsConfig{
			Executor: &ExecutorConfig{Kind: ExecutorKindNative},
		},
	}
	if native.IsExternalCLIWorker() {
		t.Error("native worker must NOT be IsExternalCLIWorker()")
	}

	// external-cli worker — must be true.
	ext := AgentConfig{
		ID:   "w-ext",
		Type: AgentTypeWorker,
		Subagents: &SubagentsConfig{
			Executor: &ExecutorConfig{Kind: ExecutorKindExternalCLI},
		},
	}
	if !ext.IsExternalCLIWorker() {
		t.Error("external-cli worker must be IsExternalCLIWorker()")
	}

	// non-worker with external-cli executor — must be false (not a worker).
	nonWorker := AgentConfig{
		ID:   "main-agent",
		Type: AgentTypeCustom,
		Subagents: &SubagentsConfig{
			Executor: &ExecutorConfig{Kind: ExecutorKindExternalCLI},
		},
	}
	if nonWorker.IsExternalCLIWorker() {
		t.Error("non-worker agent must NOT be IsExternalCLIWorker() even with external-cli executor")
	}

	// worker with nil Subagents — must be false.
	noSubagents := AgentConfig{ID: "w-nil", Type: AgentTypeWorker}
	if noSubagents.IsExternalCLIWorker() {
		t.Error("worker with nil Subagents must NOT be IsExternalCLIWorker()")
	}

	// worker with non-nil Subagents but nil Executor — must be false.
	noExecutor := AgentConfig{
		ID:        "w-noexec",
		Type:      AgentTypeWorker,
		Subagents: &SubagentsConfig{},
	}
	if noExecutor.IsExternalCLIWorker() {
		t.Error("worker with nil Executor must NOT be IsExternalCLIWorker()")
	}
}

// TestIsExternalCLIWorkerID verifies Config.IsExternalCLIWorkerID lookup by ID.
func TestIsExternalCLIWorkerID(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				{
					ID:   "native-worker",
					Type: AgentTypeWorker,
					Subagents: &SubagentsConfig{
						Executor: &ExecutorConfig{Kind: ExecutorKindNative},
					},
				},
				{
					ID:   "ext-worker",
					Type: AgentTypeWorker,
					Subagents: &SubagentsConfig{
						Executor: &ExecutorConfig{Kind: ExecutorKindExternalCLI},
					},
				},
				{
					ID:   "main",
					Type: AgentTypeCustom,
				},
			},
		},
	}

	if cfg.IsExternalCLIWorkerID("ext-worker") != true {
		t.Error("ext-worker must return true from IsExternalCLIWorkerID")
	}
	if cfg.IsExternalCLIWorkerID("native-worker") != false {
		t.Error("native-worker must return false from IsExternalCLIWorkerID")
	}
	if cfg.IsExternalCLIWorkerID("main") != false {
		t.Error("main (non-worker) must return false from IsExternalCLIWorkerID")
	}
	if cfg.IsExternalCLIWorkerID("unknown-id") != false {
		t.Error("unknown agent ID must return false from IsExternalCLIWorkerID")
	}
	if cfg.IsExternalCLIWorkerID("") != false {
		t.Error("empty agent ID must return false from IsExternalCLIWorkerID")
	}

	// nil config must not panic.
	var nilCfg *Config
	if nilCfg.IsExternalCLIWorkerID("ext-worker") != false {
		t.Error("nil Config must return false from IsExternalCLIWorkerID")
	}
}

// TestIsWorkerAndChatTarget verifies the worker/system classification helpers.
func TestIsWorkerAndChatTarget(t *testing.T) {
	worker := AgentConfig{ID: "w", Type: AgentTypeWorker}
	if !worker.IsWorker() {
		t.Fatal("AgentTypeWorker agent must report IsWorker()==true")
	}
	if worker.IsChatTarget() {
		t.Fatal("a worker must NOT be a chat target")
	}

	// ADR-049 D3: a System Agent (Type=system) is NOT a chat target either — it
	// is excluded from default-fallback/routing/delegation/team enumeration, so
	// IsChatTarget()==false, exactly like a worker.
	system := AgentConfig{ID: "judge", Type: AgentTypeSystem}
	if system.IsWorker() {
		t.Fatal("a System Agent must report IsWorker()==false")
	}
	if !system.IsSystem() {
		t.Fatal("AgentTypeSystem agent must report IsSystem()==true")
	}
	if system.IsChatTarget() {
		t.Fatal("a System Agent must NOT be a chat target (ADR-049 D3)")
	}

	// Every OTHER non-worker type is a chat target.
	for _, ty := range []AgentType{AgentTypeCore, AgentTypeCustom, ""} {
		a := AgentConfig{ID: "a", Type: ty}
		if a.IsWorker() {
			t.Fatalf("non-worker type %q must report IsWorker()==false", ty)
		}
		if !a.IsChatTarget() {
			t.Fatalf("non-worker, non-system type %q must be a chat target", ty)
		}
	}
}

// TestRepairMultipleDefaults_ClearsWorkerDefault verifies that a worker marked
// Default=true is demoted UNCONDITIONALLY — even when it is the sole default —
// because a worker can never be the routing default.
func TestRepairMultipleDefaults_ClearsWorkerDefault(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				// Sole default, but a worker → must be cleared.
				{ID: "worker", Type: AgentTypeWorker, Default: true},
				{ID: "mia", Type: AgentTypeCore, Default: false},
			},
		},
	}
	RepairMultipleDefaults(cfg)

	for _, a := range cfg.Agents.List {
		if a.ID == "worker" && a.Default {
			t.Fatal("RepairMultipleDefaults must clear Default on a worker even when it is the sole default")
		}
	}
}

// TestRepairMultipleDefaults_WorkerDemotedThenKeepFirstNonWorker verifies the
// worker is demoted BEFORE the at-most-one repair, so a worker never "wins" the
// keep-first slot over a real chat-target agent.
func TestRepairMultipleDefaults_WorkerDemotedThenKeepFirstNonWorker(t *testing.T) {
	cfg := &Config{
		Agents: AgentsConfig{
			List: []AgentConfig{
				// Worker is first in list order and marked default.
				{ID: "worker", Type: AgentTypeWorker, Default: true},
				{ID: "mia", Type: AgentTypeCore, Default: true},
			},
		},
	}
	RepairMultipleDefaults(cfg)

	var defaults []string
	for _, a := range cfg.Agents.List {
		if a.Default {
			defaults = append(defaults, a.ID)
		}
	}
	if len(defaults) != 1 {
		t.Fatalf("expected exactly one default after repair, got %v", defaults)
	}
	if defaults[0] != "mia" {
		t.Fatalf("expected the non-worker (mia) to keep default, got %q", defaults[0])
	}
}
