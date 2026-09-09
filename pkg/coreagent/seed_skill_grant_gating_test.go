// Omnipus — Core Agents
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-072 (skill activation and loading) D5.1: SeedConfig's per-agent
// skill-grant seed runs ONLY on a fresh install (isFreshInstall gate), never
// on a subsequent boot — because under D5 an empty/absent grant list is a
// valid, deliberate "operator granted nothing" state, not "never configured".
// Traces to docs/internal/specs/skill-activation-and-loading-spec.md
// FR-032/FR-033/FR-034/FR-035 and to §5.1's "must not restore a grant list
// the operator has deliberately emptied" prohibition. Test rows 18/19/20 in
// the spec's test table (§"Traceability").

package coreagent_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// TestSeedConfig_FreshInstallSeedsCoreGrants (spec test 18, FR-034, tag T1c
// "First boot seeds") — on a genuinely empty config (no agents present yet,
// the hallmark of a first boot), SeedConfig seeds each base core agent's
// skill-grant allowlist with its compiled-in roster.
func TestSeedConfig_FreshInstallSeedsCoreGrants(t *testing.T) {
	cfg := &config.Config{} // no agents.list at all — fresh install

	modified := coreagent.SeedConfig(cfg)
	require.True(t, modified, "SeedConfig must report a change on a fresh install")

	byID := map[string]config.AgentConfig{}
	for _, ac := range cfg.Agents.List {
		byID[ac.ID] = ac
	}

	// The base roster's compiled-in skill grants (documented alongside
	// coreAgentSkills in pkg/coreagent/core.go and independently confirmed by
	// pkg/coreagent/skill_allowlist_seed_test.go's FR-9.4 comment, which
	// predates D5.1 but seeds the identical mapping).
	want := map[string][]string{
		"mia": {"daily-briefing", "define-goal", "summarize"},
		"ray": {"define-goal", "summarize"},
		"jim": {"define-goal", "plan"},
		"ava": {"define-goal", "skill-authoring"},
	}

	for id, wantSkills := range want {
		ac, found := byID[id]
		require.True(t, found, "core agent %q must be seeded on a fresh install", id)
		assert.ElementsMatch(t, wantSkills, ac.Skills,
			"core agent %q must receive its compiled-in skill grant on first boot", id)
	}
}

// TestSeedConfig_TwiceWithEmptiedListStaysEmpty (spec test 19, FR-032/033/034,
// **CRIT-002 regression**) — the defect this gate exists to fix: SeedConfig
// used to re-run its skill-grant seed unconditionally on every boot, so an
// operator who deliberately emptied an agent's grant list (D5's valid "grant
// nothing" state) would find it silently restored on the next restart. This
// asserts a second SeedConfig call — after the grant list has been emptied —
// leaves it empty.
func TestSeedConfig_TwiceWithEmptiedListStaysEmpty(t *testing.T) {
	cfg := &config.Config{}

	// First boot: fresh install, seeds Mia's grant list.
	require.True(t, coreagent.SeedConfig(cfg))

	miaIdx := -1
	for i, ac := range cfg.Agents.List {
		if ac.ID == "mia" {
			miaIdx = i
		}
	}
	require.GreaterOrEqual(t, miaIdx, 0, "mia must exist after the first SeedConfig call")
	require.NotEmpty(t, cfg.Agents.List[miaIdx].Skills, "precondition: mia's grant list must be non-empty after first boot")

	// Operator deliberately empties the grant list (D5: empty means "grant
	// nothing", a valid and intentional state, not "never configured").
	cfg.Agents.List[miaIdx].Skills = []string{}

	// Second boot: agents already exist, so this is NOT a fresh install.
	coreagent.SeedConfig(cfg)

	var miaAfter config.AgentConfig
	for _, ac := range cfg.Agents.List {
		if ac.ID == "mia" {
			miaAfter = ac
		}
	}
	assert.Empty(t, miaAfter.Skills,
		"a grant list the operator deliberately emptied MUST NOT be restored by a later SeedConfig call — CRIT-002")
}

// TestSeedConfig_StillReEnforcesIdentityFields (spec test 20, FR-035,
// "(regression) — Gating didn't disable tamper protection") — the D5.1 fix
// gates ONLY the skill-grant seed behind isFreshInstall. It must not have
// accidentally weakened the pre-existing, always-on re-enforcement of a core
// agent's non-skill identity fields (name, description, color, icon, locked),
// which SeedConfig performs on every boot regardless of freshness.
func TestSeedConfig_StillReEnforcesIdentityFields(t *testing.T) {
	cfg := &config.Config{}

	// First boot: fresh install, seeds the full roster.
	require.True(t, coreagent.SeedConfig(cfg))

	canonical := coreagent.ByID(coreagent.IDMia)
	require.NotNil(t, canonical, "mia must be a known core agent")

	miaIdx := -1
	for i, ac := range cfg.Agents.List {
		if ac.ID == "mia" {
			miaIdx = i
		}
	}
	require.GreaterOrEqual(t, miaIdx, 0, "mia must exist after the first SeedConfig call")

	// Tamper with identity fields only — leave the (already non-empty) skill
	// grant list untouched so this test isolates identity re-enforcement from
	// the skill-grant gate exercised by the other two tests in this file.
	cfg.Agents.List[miaIdx].Name = "Tampered Name"
	cfg.Agents.List[miaIdx].Description = "tampered description"
	cfg.Agents.List[miaIdx].Color = "#000000"
	cfg.Agents.List[miaIdx].Icon = "bug"
	cfg.Agents.List[miaIdx].Locked = false

	// Second boot: agents already exist, so this is NOT a fresh install — the
	// skill-grant seed above would be skipped, but identity re-enforcement
	// must still run.
	coreagent.SeedConfig(cfg)

	var miaAfter config.AgentConfig
	for _, ac := range cfg.Agents.List {
		if ac.ID == "mia" {
			miaAfter = ac
		}
	}

	assert.Equal(t, canonical.Name, miaAfter.Name, "name must be re-enforced on every boot, fresh install or not")
	assert.Equal(t, canonical.Description, miaAfter.Description, "description must be re-enforced on every boot, fresh install or not")
	assert.Equal(t, canonical.Color, miaAfter.Color, "color must be re-enforced on every boot, fresh install or not")
	assert.Equal(t, canonical.Icon, miaAfter.Icon, "icon must be re-enforced on every boot, fresh install or not")
	assert.True(t, miaAfter.Locked, "locked must be re-enforced on every boot, fresh install or not")
}
