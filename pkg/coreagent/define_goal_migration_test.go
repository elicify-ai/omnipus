// Omnipus — ADR-074 D4 define-done seeding/migration + ADR-080 D-SKILL
// define-done→define-goal rename-migration tests.
//
// Covers judgment-first-criteria-spec US-4 / FR-008 (spec tests 15 and 16, and
// EC-7): fresh-install seeding of the criteria-authoring grant across the
// core roster (now "define-goal" per ADR-080 D-SKILL), the one-shot
// marker-keyed additive migration for pre-ADR-074 installs (nil stays nil,
// operator-emptied [] stays empty, append happens exactly once), the
// second-boot byte-level no-op, AND the ADR-080 D-SKILL rewrite migration
// that relabels an already-granted "define-done" token to "define-goal" for
// installs that ran the ADR-074 migration before this rename shipped.
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

// TestSeedConfig_FreshInstall_DefineGoalEverywhere is spec test 15 (US-4 S1),
// updated for ADR-080 D-SKILL: a fresh install seeds the RENAMED
// "define-goal" grant onto every core-roster agent that carries an
// allowlist, PlanSupervisor gets exactly {plan, define-goal}, and BOTH
// migration markers (the historical ADR-074 one and the ADR-080 rename one)
// are recorded on the same pass so neither ever re-runs — even though a
// fresh install gives each migration nothing to actually do (the grant
// already carries its final, renamed name from coreAgentSkills/
// systemAgentSkills).
func TestSeedConfig_FreshInstall_DefineGoalEverywhere(t *testing.T) {
	cfg := &config.Config{}
	require.True(t, coreagent.SeedConfig(cfg))

	byID := map[string][]string{}
	for _, a := range cfg.Agents.List {
		byID[a.ID] = a.Skills
	}

	for _, id := range []string{"mia", "jim", "ava", "ray", "planner", "explorer", "researcher"} {
		assert.Contains(t, byID[id], "define-goal",
			"core-roster agent %q must carry the define-goal grant on a fresh install (ADR-074 D4, renamed by ADR-080 D-SKILL)", id)
		assert.NotContains(t, byID[id], "define-done",
			"a fresh install must never carry the OLD define-done token for %q", id)
	}
	// The worker's seed grants no skills; define-goal must not conjure an
	// allowlist for it (nil stays unrestricted).
	assert.Nil(t, byID["worker"], "the worker's nil (unrestricted) skill posture is unchanged")
	// The Judge consumes criteria, never authors them — no grant (ADR-074 D4).
	assert.Nil(t, byID[string(coreagent.IDJudge)], "the Judge gets NO define-goal grant")
	// PlanSupervisor: exactly the pair, canonical order, renamed.
	assert.Equal(t, []string{"plan", "define-goal"}, byID[string(coreagent.IDPlanSupervisor)])
	// Both markers recorded in the same pass.
	assert.Contains(t, cfg.SeededSkillGrants, coreagent.SkillsMigrationDefineDone,
		"the ADR-074 migration marker must be recorded on a fresh install too")
	assert.Contains(t, cfg.SeededSkillGrants, coreagent.SkillsMigrationDefineGoalRename,
		"the ADR-080 rename migration marker must be recorded on a fresh install too")
}

// TestDefineGoalMigration_FromPreADR074Install is spec test 16 (US-4 S2/S3):
// on a pre-ADR-074 existing install without either marker, ONE SeedConfig
// pass appends "define-done" to every non-nil, non-empty core-roster
// allowlist that lacks it, the ADR-080 rewrite migration immediately
// relabels that same append to "define-goal" within the SAME pass, and both
// markers are recorded; nil and operator-emptied lists stay untouched;
// user-created agents with curated allowlists are never mutated; and a
// second pass is a byte-level no-op.
func TestDefineGoalMigration_FromPreADR074Install(t *testing.T) {
	cfg := &config.Config{}
	// Simulate a pre-ADR-074 existing install: roster present with the OLD
	// seeded lists (no define-done/define-goal), no markers. Non-roster
	// shapes included: an operator-emptied list, an operator-cleared nil,
	// and a user agent.
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

	assert.Equal(t, []string{"summarize", "daily-briefing", "define-goal"}, byID["mia"],
		"non-empty roster allowlist lacking the grant gets define-done appended by the ADR-074 "+
			"migration, then rewritten to define-goal in place by the ADR-080 rename migration in "+
			"the same pass — order preserved, final token is the renamed one")
	assert.Equal(t, []string{"plan", "define-goal"}, byID["jim"])
	assert.Equal(t, []string{}, byID["ava"],
		"an operator-emptied [] allowlist is an opt-out — neither migration may touch it")
	assert.Nil(t, byID["ray"],
		"a nil (unrestricted) allowlist stays nil — unrestricted already resolves every skill")
	assert.Equal(t, []string{"summarize"}, byID["custom-agent"],
		"user-created agents with curated allowlists are NOT mutated by the ADR-074 append migration")
	assert.Contains(t, cfg.SeededSkillGrants, coreagent.SkillsMigrationDefineDone,
		"the ADR-074 marker and its append must land in the SAME SeedConfig pass")
	assert.Contains(t, cfg.SeededSkillGrants, coreagent.SkillsMigrationDefineGoalRename,
		"the ADR-080 rename marker and its rewrite must land in the SAME SeedConfig pass")

	// Second boot: byte-level no-op (both markers present → both migrations
	// skipped; everything else already canonical).
	before := marshalSeedState(t, cfg)
	assert.False(t, coreagent.SeedConfig(cfg),
		"second SeedConfig pass must report modified=false")
	after := marshalSeedState(t, cfg)
	assert.Equal(t, before, after, "second pass must be a byte-level no-op")
}

// TestDefineGoalMigration_AppendHappensExactlyOnce: an operator who REMOVES
// the criteria-authoring grant after the migration ran must not have it
// restored — the marker records that the one-shot append already happened
// (ADR-072 D5.1's prohibition holds from that point on). Exercised end to
// end (both migrations run on the same first boot, final token is
// define-goal), since that is what a real install sees.
func TestDefineGoalMigration_AppendHappensExactlyOnce(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Name: "Mia", Skills: []string{"summarize", "daily-briefing"}},
	}
	require.True(t, coreagent.SeedConfig(cfg))
	require.Contains(t, findSeeded(t, cfg, "mia").Skills, "define-goal")

	// Operator deliberately removes the grant post-migration.
	for i := range cfg.Agents.List {
		if cfg.Agents.List[i].ID == "mia" {
			cfg.Agents.List[i].Skills = []string{"summarize", "daily-briefing"}
		}
	}
	coreagent.SeedConfig(cfg)
	assert.NotContains(t, findSeeded(t, cfg, "mia").Skills, "define-done",
		"a post-migration removal is an operator choice — the ADR-074 marker must prevent a re-append")
	assert.NotContains(t, findSeeded(t, cfg, "mia").Skills, "define-goal",
		"a post-migration removal is an operator choice — the ADR-080 marker must prevent a re-grant too")
}

// TestDefineGoalMigration_PostMarkerNewCoreAgent is EC-7: a core-roster agent
// created AFTER both markers exist (e.g. a roster agent added by an upgrade,
// or one whose entity record was deleted) receives the renamed define-goal
// grant via its creation-time seed (coreAgentSkills), not via either
// migration.
func TestDefineGoalMigration_PostMarkerNewCoreAgent(t *testing.T) {
	cfg := &config.Config{}
	// Both markers already present; jim missing from the roster list entirely.
	cfg.SeededSkillGrants = []string{
		coreagent.SkillsMigrationDefineDone,
		coreagent.SkillsMigrationDefineGoalRename,
	}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Name: "Mia", Skills: []string{"summarize", "daily-briefing", "define-goal"}},
	}
	require.True(t, coreagent.SeedConfig(cfg))

	jim := findSeeded(t, cfg, "jim")
	assert.Contains(t, jim.Skills, "define-goal",
		"a core agent seeded after both markers exist must still get define-goal at creation time (EC-7)")
}

// TestDefineGoalRenameMigration_RewritesLegacyToken exercises the ADR-080
// D-SKILL rewrite migration on its own: an install that already ran the
// ADR-074 migration in a previous release (its marker present, the roster
// carrying the OLD "define-done" token) gets the token rewritten to
// "define-goal" — in place, same slot, order preserved — on the first boot
// under the new binary, and the rewrite marker is recorded.
func TestDefineGoalRenameMigration_RewritesLegacyToken(t *testing.T) {
	cfg := &config.Config{}
	cfg.SeededSkillGrants = []string{coreagent.SkillsMigrationDefineDone}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Name: "Mia", Skills: []string{"summarize", "daily-briefing", "define-done"}},
		{ID: "jim", Name: "Jim", Skills: []string{"plan", "define-done"}},
		{ID: "ava-empty", Name: "Ava2", Type: config.AgentTypeCustom, Skills: []string{}},
		{ID: "ray-nil", Name: "Ray2", Type: config.AgentTypeCustom, Skills: nil},
		{ID: "custom-with-legacy", Name: "Custom", Type: config.AgentTypeCustom,
			Skills: []string{"summarize", "define-done"}},
	}
	require.True(t, coreagent.SeedConfig(cfg))

	byID := map[string][]string{}
	for _, a := range cfg.Agents.List {
		byID[a.ID] = a.Skills
	}

	assert.Equal(t, []string{"summarize", "daily-briefing", "define-goal"}, byID["mia"],
		"the legacy token is rewritten in its existing slot, order preserved")
	assert.Equal(t, []string{"plan", "define-goal"}, byID["jim"])
	assert.Equal(t, []string{}, byID["ava-empty"],
		"an operator-emptied [] allowlist stays untouched by the rename migration too")
	assert.Nil(t, byID["ray-nil"],
		"a nil (unrestricted) allowlist stays nil")
	assert.Equal(t, []string{"summarize", "define-goal"}, byID["custom-with-legacy"],
		"the rename migration is NOT restricted to the core roster — a user-created agent's "+
			"already-granted define-done is rewritten too, since this is a pure rename, not a new grant")
	assert.Contains(t, cfg.SeededSkillGrants, coreagent.SkillsMigrationDefineGoalRename)

	// Second boot: byte-level no-op.
	before := marshalSeedState(t, cfg)
	assert.False(t, coreagent.SeedConfig(cfg))
	after := marshalSeedState(t, cfg)
	assert.Equal(t, before, after, "second pass must be a byte-level no-op")
}

// TestDefineGoalRenameMigration_AlreadyRenamedLeftAlone: an allowlist that
// already carries "define-goal" (e.g. seeded directly by a fresh install, or
// already rewritten by a prior boot before the marker got persisted) is left
// exactly alone by the rewrite migration — nothing to rewrite into, no
// duplicate introduced.
func TestDefineGoalRenameMigration_AlreadyRenamedLeftAlone(t *testing.T) {
	cfg := &config.Config{}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Name: "Mia", Skills: []string{"summarize", "daily-briefing", "define-goal"}},
	}
	require.True(t, coreagent.SeedConfig(cfg))

	mia := findSeeded(t, cfg, "mia")
	assert.Equal(t, []string{"summarize", "daily-briefing", "define-goal"}, mia.Skills,
		"an already-renamed allowlist is left byte-identical")
}

// TestDefineGoalRenameMigration_RemovalAfterRenameRespected: an operator who
// removes the grant after the rename migration ran must not have it
// restored on a later boot.
func TestDefineGoalRenameMigration_RemovalAfterRenameRespected(t *testing.T) {
	cfg := &config.Config{}
	cfg.SeededSkillGrants = []string{
		coreagent.SkillsMigrationDefineDone,
		coreagent.SkillsMigrationDefineGoalRename,
	}
	cfg.Agents.List = []config.AgentConfig{
		{ID: "mia", Name: "Mia", Skills: []string{"summarize", "daily-briefing"}}, // operator removed it
	}
	// Other core-roster agents are absent from this deliberately minimal
	// fixture and get freshly created by the normal (unrelated) seeding
	// loop, so the overall return value is not asserted here — only that
	// mia's already-removed grant is never restored by either migration.
	coreagent.SeedConfig(cfg)

	mia := findSeeded(t, cfg, "mia")
	assert.NotContains(t, mia.Skills, "define-goal",
		"the rename marker being present must not re-grant a removed skill")
	assert.NotContains(t, mia.Skills, "define-done")
}
