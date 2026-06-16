// Omnipus — sub-agent worker tier: config helpers + default repair tests.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package config

import "testing"

// TestIsWorkerAndChatTarget verifies the worker classification helpers.
func TestIsWorkerAndChatTarget(t *testing.T) {
	worker := AgentConfig{ID: "w", Type: AgentTypeWorker}
	if !worker.IsWorker() {
		t.Fatal("AgentTypeWorker agent must report IsWorker()==true")
	}
	if worker.IsChatTarget() {
		t.Fatal("a worker must NOT be a chat target")
	}

	for _, ty := range []AgentType{AgentTypeCore, AgentTypeCustom, AgentTypeSystem, ""} {
		a := AgentConfig{ID: "a", Type: ty}
		if a.IsWorker() {
			t.Fatalf("non-worker type %q must report IsWorker()==false", ty)
		}
		if !a.IsChatTarget() {
			t.Fatalf("non-worker type %q must be a chat target", ty)
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
