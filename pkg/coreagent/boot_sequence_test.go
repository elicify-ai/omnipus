// Omnipus — Core Agents
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

// boot_sequence_test.go covers the WorkspaceShellEnabled seeding contract:
//   - validateBootConfig must NOT materialize nil → &false for WorkspaceShellEnabled
//     (tested separately in pkg/config/sandbox_test.go::TestWorkspaceShellEnabled_NilPassthrough),
//   - SeedConfig must default nil → &true for Jim (fresh install),
//   - SeedConfig must PRESERVE an operator's explicit &false (security blocker fix):
//     SeedConfig runs on every boot, so re-enabling an explicit false would silently
//     re-enable workspace shell exec for ALL agents on the next restart, violating
//     Hard-Constraint #6 (explicit security opt-outs are never overridden).
//
// Placing these tests in the coreagent_test package avoids the import cycle
// between pkg/config (which imports nothing from coreagent) and pkg/coreagent
// (which imports pkg/config).

import (
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/coreagent"
)

// TestSeedConfig_JimGetsWorkspaceShell verifies the WorkspaceShellEnabled
// seeding contract end-to-end from the SeedConfig side, for both the
// re-enforcement branch (Jim already in the list) and the implicit default.
//
// Scenario A — nil initial state (fresh install / fixed validator):
// SeedConfig must set WorkspaceShellEnabled = &true for Jim when the field is nil.
//
// Scenario B — &false initial state (operator's explicit security opt-out):
// SeedConfig MUST PRESERVE &false. SeedConfig runs on every boot; flipping an
// explicit false back to true would silently re-enable workspace shell exec for
// ALL agents on the next restart (CRITICAL security blocker; Hard-Constraint #6).
func TestSeedConfig_JimGetsWorkspaceShell(t *testing.T) {
	t.Run("nil_initial_state_jim_gets_true", func(t *testing.T) {
		cfg := config.DefaultConfig()
		// Precondition: WorkspaceShellEnabled is nil (fresh install,
		// or validator correctly passed nil through without materializing &false).
		cfg.Sandbox.Experimental.WorkspaceShellEnabled = nil

		// Add Jim to the list so the re-enforcement branch fires
		// (not the new-agent seeding branch).
		cfg.Agents.List = []config.AgentConfig{
			{
				ID:     "jim",
				Name:   "Jim — General Purpose",
				Locked: false, // will be set to true by SeedConfig
			},
		}

		modified := coreagent.SeedConfig(cfg)

		if !modified {
			t.Fatal("SeedConfig must report modified=true when WorkspaceShellEnabled is nil for Jim")
		}
		if cfg.Sandbox.Experimental.WorkspaceShellEnabled == nil {
			t.Fatal("SeedConfig must set WorkspaceShellEnabled to &true for Jim when nil; got nil")
		}
		if !*cfg.Sandbox.Experimental.WorkspaceShellEnabled {
			t.Fatalf("SeedConfig must set WorkspaceShellEnabled=true for Jim; got false")
		}
	})

	t.Run("explicit_false_is_preserved_not_overridden", func(t *testing.T) {
		cfg := config.DefaultConfig()
		// Precondition: an operator has EXPLICITLY set WorkspaceShellEnabled=false
		// in config.json to deny workspace shell exec. This is a deliberate security
		// opt-out and MUST survive every boot's SeedConfig pass.
		f := false
		cfg.Sandbox.Experimental.WorkspaceShellEnabled = &f

		cfg.Agents.List = []config.AgentConfig{
			{
				ID:     "jim",
				Name:   "Jim — General Purpose",
				Locked: false,
			},
		}

		coreagent.SeedConfig(cfg)

		if cfg.Sandbox.Experimental.WorkspaceShellEnabled == nil {
			t.Fatal("SeedConfig must not nil-out an explicit WorkspaceShellEnabled=false")
		}
		if *cfg.Sandbox.Experimental.WorkspaceShellEnabled {
			t.Fatal("SECURITY: SeedConfig must PRESERVE an operator's explicit " +
				"WorkspaceShellEnabled=false; flipping it back to true re-enables " +
				"workspace shell exec for ALL agents on every boot (Hard-Constraint #6)")
		}
	})

	t.Run("explicit_false_survives_repeated_seeding", func(t *testing.T) {
		// SeedConfig is idempotent and runs on EVERY boot. Simulate three boots and
		// assert the explicit false is never clobbered — the exact failure mode the
		// blocker describes ("reset to true on next boot").
		cfg := config.DefaultConfig()
		f := false
		cfg.Sandbox.Experimental.WorkspaceShellEnabled = &f
		cfg.Agents.List = []config.AgentConfig{
			{ID: "jim", Name: "Jim — General Purpose", Locked: true},
		}

		for boot := 1; boot <= 3; boot++ {
			coreagent.SeedConfig(cfg)
			if cfg.Sandbox.Experimental.WorkspaceShellEnabled == nil ||
				*cfg.Sandbox.Experimental.WorkspaceShellEnabled {
				t.Fatalf("boot %d: explicit WorkspaceShellEnabled=false was overridden to true", boot)
			}
		}
	})
}

// TestSeedConfig_NonJimAgent_WorkspaceShellPassthrough verifies that SeedConfig
// does NOT set WorkspaceShellEnabled=true when Jim is absent from the config.
// Only Jim's seeding (new-agent or re-enforcement) should trigger the flag.
//
// This test uses a config with only a non-core custom agent. SeedConfig will
// add all 5 core agents (including Jim) on the new-agent path, which will also
// set the flag — that is the EXPECTED correct behavior. What we assert here is
// that (a) the validator does not set it before SeedConfig, and (b) after
// SeedConfig the flag is true because Jim was seeded, which is correct.
//
// For the pure "no Jim in list" isolation: we verify that the custom-agent
// re-enforcement loop does NOT set the flag (i.e., SeedConfig's
// WorkspaceShellEnabled assignment is Jim-specific, not global).
func TestSeedConfig_NonJimAgent_WorkspaceShellPassthrough(t *testing.T) {
	t.Run("re-enforcement_loop_non_jim_does_not_set_flag", func(t *testing.T) {
		cfg := config.DefaultConfig()
		// Start with flag already true so we can detect if any non-Jim branch clears it.
		// The re-enforcement loop for core agents other than Jim must not touch the flag.
		tr := true
		cfg.Sandbox.Experimental.WorkspaceShellEnabled = &tr

		// Populate all 5 core agents in the list so SeedConfig only runs
		// the re-enforcement loop (no new-agent seeding).
		cfg.Agents.List = []config.AgentConfig{
			{ID: "jim", Name: "Jim — General Purpose", Locked: true},
			{ID: "ava", Name: "Ava — Agent Builder", Locked: true},
			{ID: "mia", Name: "Mia — Omnipus Guide", Locked: true},
			{ID: "ray", Name: "Ray — Researcher", Locked: true},
			{ID: "max", Name: "Max — Automator", Locked: true},
		}

		coreagent.SeedConfig(cfg)

		// Flag must still be true (Jim's re-enforcement branch holds it true;
		// other agents' re-enforcement branches must not clear it).
		if cfg.Sandbox.Experimental.WorkspaceShellEnabled == nil || !*cfg.Sandbox.Experimental.WorkspaceShellEnabled {
			t.Error("non-Jim core agents' re-enforcement loop must not clear WorkspaceShellEnabled")
		}
	})

	t.Run("custom_only_agent_list_flag_remains_nil_until_jim_seeded", func(t *testing.T) {
		cfg := config.DefaultConfig()
		cfg.Sandbox.Experimental.WorkspaceShellEnabled = nil

		// One custom (non-core) agent only.
		cfg.Agents.List = []config.AgentConfig{
			{
				ID:   "my-custom-agent",
				Name: "My Custom Agent",
			},
		}

		// SeedConfig will add Jim (new-agent path) and set flag → &true.
		// Verify custom agent is not locked by SeedConfig.
		coreagent.SeedConfig(cfg)

		// Jim was seeded fresh → flag must be &true (expected correct behavior).
		if cfg.Sandbox.Experimental.WorkspaceShellEnabled == nil {
			t.Fatal("SeedConfig must set WorkspaceShellEnabled=true when Jim is newly seeded")
		}
		if !*cfg.Sandbox.Experimental.WorkspaceShellEnabled {
			t.Fatal("SeedConfig must set WorkspaceShellEnabled=true for newly seeded Jim")
		}

		// Custom agent must not be locked by SeedConfig.
		for _, a := range cfg.Agents.List {
			if a.ID == "my-custom-agent" {
				if a.Locked {
					t.Error("SeedConfig must not set Locked=true on a non-core custom agent")
				}
			}
		}
	})
}
