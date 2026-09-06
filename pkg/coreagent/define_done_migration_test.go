// Omnipus — ADR-074 D4 define-done seeding + migration tests.
//
// Covers judgment-first-criteria-spec US-4 / FR-008 (spec tests 15 and 16, and
// EC-7): fresh-install seeding of the define-done grant across the core
// roster, the one-shot marker-keyed additive migration for existing installs
// (nil stays nil, operator-emptied [] stays empty, append happens exactly
// once), and the second-boot byte-level no-op.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package coreagent_test

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
)

// marshalSeedState serializes everything the seed/migration can touch — the
// roster (json:"-" on Agents.List keeps it out of Config's own marshal) plus
// the whole Config struct (which carries seeded_skill_grants) — so byte-level
// comparison sees appends AND the marker.
func marshalSeedState(t *testing.T, cfg *config.Config) []byte {
	t.Helper()
	state := struct {
		Config *config.Config       `json:"config"`
		Roster []config.AgentConfig `json:"roster"`
	}{cfg, cfg.Agents.List}
	b, err := json.Marshal(state)
	require.NoError(t, err)
	return b
}

// TestSeedConfig_FreshInstall_DefineDoneEverywhere is spec test 15 (US-4 S1):
// a fresh install seeds the define-done grant onto every core-roster agent
// that carries an allowlist, PlanSupervisor gets exactly {plan, define-done},
// and the migration marker is recorded so no later boot re-runs the append.
func TestSeedConfig_FreshInstall_DefineDoneEverywhere(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	byID := map[string][]string{}
	for _, a := range cfg.Agents.List {
		byID[a.ID] = a.Skills
	}

	for _, id := range []string{"mia", "jim", "ava", "ray", "planner", "explorer", "researcher"} {
		assert.Contains(t, byID[id], "define-done",
			"core-roster agent %q must carry the define-done grant on a fresh install (ADR-074 D4)", id)
	}
	// The worker's seed grants no skills; define-done must not conjure an
	// allowlist for it (nil stays unrestricted).
	assert.Nil(t, byID["worker"], "the worker's nil (unrestricted) skill posture is unchanged")
	// The Judge consumes criteria, never authors them — no grant (ADR-074 D4).
	assert.Nil(t, byID[string(coreagent.IDJudge)], "the Judge gets NO define-done grant")
	// PlanSupervisor: exactly the pair, canonical order.
	assert.Equal(t, []string{"plan", "define-done"}, byID[string(coreagent.IDPlanSupervisor)])
	// Marker recorded in the same pass.
	assert.Contains(t, cfg.SeededSkillGrants, coreagent.SkillsMigrationDefineDone,
		"the migration marker must be recorded on a fresh install too")
}

// TestDefineDoneMigration_ExistingInstall is spec test 16 (US-4 S2/S3): on an
// existing install without the marker, ONE SeedConfig pass appends define-done
// to every non-nil, non-empty core-roster allowlist that lacks it AND records
// the marker; nil and operator-emptied lists stay untouched; user-created
// agents with curated allowlists are never mutated; and a second pass is a
// byte-level no-op.
func TestDefineDoneMigration_ExistingInstall(t *testing.T) {
	cfg := &config.Config{}
	// Simulate a pre-ADR-074 existing install: roster present with the OLD
	// seeded lists (no define-done), no marker. Non-roster shapes included:
	// an operator-emptied list, an operator-cleared nil, and a user agent.
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Name: "Mia", Skills: []string{"summarize", "daily-briefing"}},
		{ID: "jim", Name: "Jim", Skills: []string{"plan"}},
		{ID: "ava", Name: "Ava", Skills: []string{}}, // operator opted out — stays empty
		{ID: "ray", Name: "Ray", Skills: nil},        // unrestricted — stays nil
		{ID: "custom-agent", Name: "Custom", Type: config.AgentTypeCustom, Skills: []string{"summarize"}},
	}
	require.True(t, coreagent.SeedConfig(cfg))

	byID := map[string][]string{}
	for _, a := range cfg.Agents.List {
		byID[a.ID] = a.Skills
	}

	assert.Equal(t, []string{"summarize", "daily-briefing", "define-done"}, byID["mia"],
		"non-empty roster allowlist lacking define-done gets it APPENDED, order preserved")
	assert.Equal(t, []string{"plan", "define-done"}, byID["jim"])
	assert.Equal(t, []string{}, byID["ava"],
		"an operator-emptied [] allowlist is an opt-out — the migration must respect it")
	assert.Nil(t, byID["ray"],
		"a nil (unrestricted) allowlist stays nil — unrestricted already resolves every skill")
	assert.Equal(t, []string{"summarize"}, byID["custom-agent"],
		"user-created agents with curated allowlists are NOT mutated by the migration")
	assert.Contains(t, cfg.SeededSkillGrants, coreagent.SkillsMigrationDefineDone,
		"marker and appends must land in the SAME SeedConfig pass")

	// Second boot: byte-level no-op (marker present → migration skipped;
	// everything else already canonical).
	before := marshalSeedState(t, cfg)
	assert.False(t, coreagent.SeedConfig(cfg),
		"second SeedConfig pass must report modified=false")
	after := marshalSeedState(t, cfg)
	assert.Equal(t, before, after, "second pass must be a byte-level no-op")
}

// TestDefineDoneMigration_AppendHappensExactlyOnce: an operator who REMOVES
// define-done after the migration ran must not have it restored — the marker
// records that the one-shot append already happened (ADR-072 D5.1's
// prohibition holds from that point on).
func TestDefineDoneMigration_AppendHappensExactlyOnce(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Name: "Mia", Skills: []string{"summarize", "daily-briefing"}},
	}
	require.True(t, coreagent.SeedConfig(cfg))
	require.Contains(t, findSeeded(t, cfg, "mia").Skills, "define-done")

	// Operator deliberately removes the grant post-migration.
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "mia" {
			cfg.Agents.List[i].Skills = []string{"summarize", "daily-briefing"}
		}
	}
	coreagent.SeedConfig(cfg)
	assert.NotContains(t, findSeeded(t, cfg, "mia").Skills, "define-done",
		"a post-migration removal is an operator choice — the marker must prevent a re-append")
}

// TestDefineDoneMigration_PostMarkerNewCoreAgent is EC-7: a core-roster agent
// created AFTER the marker exists (e.g. a roster agent added by an upgrade, or
// one whose entity record was deleted) receives define-done via its
// creation-time seed (coreAgentSkills), not via the migration.
func TestDefineDoneMigration_PostMarkerNewCoreAgent(t *testing.T) {
	cfg := &config.Config{}
	// Marker already present; jim missing from the roster list entirely.
	cfg.SeededSkillGrants = []string{coreagent.SkillsMigrationDefineDone}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Name: "Mia", Skills: []string{"summarize", "daily-briefing", "define-done"}},
	}
	require.True(t, coreagent.SeedConfig(cfg))

	jim := findSeeded(t, cfg, "jim")
	assert.Contains(t, jim.Skills, "define-done",
		"a core agent seeded after the marker exists must still get define-done at creation time (EC-7)")
}
