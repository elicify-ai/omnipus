// gateway_boot_roster_test.go: tests for load souls and seed the agent roster at boot, persisting what the seed wrote

package gateway

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agentstore"
	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/coreagent"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- moved from gateway_boot.go tests 2026-09-15 ---

// TestDeleteOrphanedDefineDoneDir_MarkerAndReplacementPresent_DeletesOrphan
// is case (a): marker present + define-goal/ present + define-done/ present
// => define-done/ deleted.
func TestDeleteOrphanedDefineDoneDir_MarkerAndReplacementPresent_DeletesOrphan(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	mustMkdirSkill(t, skillsGlobalDir, "define-goal")
	doneDir := mustMkdirSkill(t, skillsGlobalDir, "define-done")
	markers := []string{coreagent.SkillsMigrationDefineGoalRename}

	deleted, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !deleted {
		t.Fatal("expected deleted=true when define-goal/ is present and define-done/ exists")
	}
	requireDirAbsent(t, doneDir)
}

// TestDeleteOrphanedDefineDoneDir_ReplacementAbsent_PreservesOrphan is case
// (b): marker present + define-goal/ ABSENT => define-done/ PRESERVED, no
// delete. This is the fail-open scenario the fix closes: a partial/failed
// SeedDefaults must never cost the operator their quality bar.
func TestDeleteOrphanedDefineDoneDir_ReplacementAbsent_PreservesOrphan(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	// Deliberately do NOT create define-goal/ — simulates SeedDefaults
	// failing after the migration marker was already recorded.
	doneDir := mustMkdirSkill(t, skillsGlobalDir, "define-done")
	markers := []string{coreagent.SkillsMigrationDefineGoalRename}

	deleted, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err == nil {
		t.Fatal("expected a non-nil error when the replacement define-goal/ directory is absent")
	}
	if deleted {
		t.Fatal("expected deleted=false when the replacement define-goal/ directory is absent")
	}
	requireDirExists(t, doneDir)
}

// TestDeleteOrphanedDefineDoneDir_SecondCall_CleanNoOp is case (c): a second
// call after the orphan has already been deleted is a clean no-op — no
// error, deleted=false — idempotent by the directories' own on-disk state.
func TestDeleteOrphanedDefineDoneDir_SecondCall_CleanNoOp(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	mustMkdirSkill(t, skillsGlobalDir, "define-goal")
	mustMkdirSkill(t, skillsGlobalDir, "define-done")
	markers := []string{coreagent.SkillsMigrationDefineGoalRename}

	first, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err != nil || !first {
		t.Fatalf("setup: first call must delete cleanly, got deleted=%v err=%v", first, err)
	}

	second, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, markers)
	if err != nil {
		t.Fatalf("second call must be a clean no-op, got error: %v", err)
	}
	if second {
		t.Fatal("second call must report deleted=false — define-done/ was already gone")
	}
}

// TestDeleteOrphanedDefineDoneDir_MarkerAbsent_NeverTouchesEitherDir covers
// a pre-ADR-080 install that has not run the rename yet: the marker is
// absent, so neither directory is touched regardless of what's on disk.
func TestDeleteOrphanedDefineDoneDir_MarkerAbsent_NeverTouchesEitherDir(t *testing.T) {
	skillsGlobalDir := t.TempDir()
	doneDir := mustMkdirSkill(t, skillsGlobalDir, "define-done")

	deleted, err := deleteOrphanedDefineDoneDir(skillsGlobalDir, nil)
	if err != nil {
		t.Fatalf("unexpected error with no marker present: %v", err)
	}
	if deleted {
		t.Fatal("expected deleted=false with no migration marker present")
	}
	requireDirExists(t, doneDir)
}

// TestSeedSystemAgentEagerSouls_PlanSupervisorRubricReachesDisk is the direct
// regression test for the FR-005 gap: PlanSupervisorDefaultRubric existed only
// as a Go constant that no write path ever materialized, because both seed
// paths were hardcoded to the Judge.
//
// newSeededJudgeAPI runs the REAL boot sequence (coreagent.SeedConfig then
// seedSystemAgentEagerSouls), so this reads the actual file the actual boot
// wrote — no re-implementation, no assertion that a function ran.
func TestSeedSystemAgentEagerSouls_PlanSupervisorRubricReachesDisk(t *testing.T) {
	require.NotEmpty(t, strings.TrimSpace(coreagent.PlanSupervisorDefaultRubric),
		"the rubric constant itself must be non-empty, or 'it reached disk' would be vacuous")

	api := newSeededJudgeAPI(t)
	cfg := api.agentLoop.GetConfig()
	soulPath := planSupervisorSoulPath(t, cfg, api.homePath)

	onDisk, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr,
		"PlanSupervisor SOUL.md must exist immediately after boot — there is no lazy backstop (FR-005 rev 2), "+
			"so a missing file here means the adjudicator would run on an EMPTY prompt")
	assert.Equal(t, coreagent.PlanSupervisorDefaultRubric, string(onDisk),
		"the on-disk soul must be the compiled adjudication rubric, byte for byte")

	// The same boot must still seed the Judge — generalising the loop must not
	// have traded one hardcoded agent for another.
	judgeWS, wsErr := agentWorkspacePath(cfg, string(coreagent.IDJudge), "", api.homePath)
	require.NoError(t, wsErr)
	judgeSoul, judgeErr := os.ReadFile(filepath.Join(judgeWS, "SOUL.md"))
	require.NoError(t, judgeErr, "the Judge's soul must still be seeded by the same boot")
	assert.Equal(t, coreagent.JudgeDefaultRubric, string(judgeSoul))

	// And the operator must actually SEE it: getAgent reads SOUL.md from the
	// workspace and (ac.IsSystem()) does not blank it out for a locked System
	// Agent, so a fresh install shows the standards it is running under.
	w := httptest.NewRecorder()
	api.getAgent(w, string(coreagent.IDPlanSupervisor))
	require.Equal(t, http.StatusOK, w.Code, "GET plansupervisor; body=%s", w.Body.String())
	var got gen.Agent
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &got))
	assert.Equal(t, coreagent.PlanSupervisorDefaultRubric, got.Soul,
		"GET /api/v1/agents/plansupervisor on a fresh install must show the default rubric, not an empty soul")
	assert.Equal(t, gen.AgentTypeSystem, got.Type)
	assert.True(t, got.Locked)
}

// TestSeedSystemAgentEagerSouls_PreservesOperatorEditedPlanSupervisorSoul
// locks the operator-editable contract: seedSystemAgents re-enforces
// identity/type/locked/tool-policy on EVERY boot, but Model/Provider and the
// SOUL are preserved once written. Since the eager seed now runs on every boot
// (not just the first), an overwrite here would silently revert an operator's
// tuned rubric on the next restart.
func TestSeedSystemAgentEagerSouls_PreservesOperatorEditedPlanSupervisorSoul(t *testing.T) {
	api := newSeededJudgeAPI(t)
	cfg := api.agentLoop.GetConfig()
	soulPath := planSupervisorSoulPath(t, cfg, api.homePath)

	// The edit must land on a file the FIRST boot actually seeded — otherwise
	// this test would pass vacuously against a build where the eager seed
	// never writes the PlanSupervisor's soul at all.
	seeded, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr, "the first boot must have seeded the soul before the operator edits it")
	require.Equal(t, coreagent.PlanSupervisorDefaultRubric, string(seeded))

	const editedSoul = "You are the Plan Supervisor. House rule: never supersede a member on a first failure."
	require.NoError(t, os.WriteFile(soulPath, []byte(editedSoul), 0o644),
		"simulate an operator editing the PlanSupervisor's soul")

	// Simulate a full restart: the config re-seed (tamper protection /
	// identity repair) followed by the eager soul seed, in the same order
	// RunContextWithOptions runs them.
	coreagent.SeedConfig(cfg)
	seedSystemAgentEagerSouls(cfg)

	after, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr, "SOUL.md must still exist after a restart")
	assert.Equal(t, editedSoul, string(after),
		"a restart must NOT overwrite an operator-edited PlanSupervisor soul with the compiled default")

	// A second restart must be equally inert (the guard is content-based, not
	// a once-only flag).
	coreagent.SeedConfig(cfg)
	seedSystemAgentEagerSouls(cfg)
	afterSecond, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr)
	assert.Equal(t, editedSoul, string(afterSecond),
		"a second restart must still preserve the operator-edited soul")
}

// TestSeedSystemAgentEagerSouls_PlanSupervisorZeroByteSoulIsBackfilled proves a
// 0-byte SOUL.md (an interrupted write, or a hand-created empty file) counts as
// MISSING, not as "the operator wants an empty prompt" — mirroring the Judge's
// own rule. Without this, the one path that can give the adjudicator a prompt
// would consider a blank file already-seeded forever.
func TestSeedSystemAgentEagerSouls_PlanSupervisorZeroByteSoulIsBackfilled(t *testing.T) {
	tmpDir := t.TempDir()
	// seedSystemAgentEagerSouls resolves each workspace via $OMNIPUS_HOME
	// (agent.ResolveAgentHome), not via the tmpDir threaded into
	// agentWorkspacePath — pin them to the same directory.
	t.Setenv("OMNIPUS_HOME", tmpDir)
	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 8080},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home: tmpDir, DefaultModel: config.DefaultModel{Model: "test-model"}, MaxTokens: 4096},
		},
	}
	coreagent.SeedConfig(cfg)

	soulPath := planSupervisorSoulPath(t, cfg, tmpDir)
	require.NoError(t, os.MkdirAll(filepath.Dir(soulPath), 0o755))
	require.NoError(t, os.WriteFile(soulPath, []byte{}, 0o644),
		"seed a 0-byte SOUL.md before the eager seed runs")

	seedSystemAgentEagerSouls(cfg)

	got, readErr := os.ReadFile(soulPath)
	require.NoError(t, readErr)
	assert.Equal(t, coreagent.PlanSupervisorDefaultRubric, string(got),
		"a 0-byte PlanSupervisor SOUL.md must be treated as missing and backfilled")
}

// TestSeedSystemAgentEagerSouls_SeedsEverySystemAgentWithADefaultSoul is the
// matching generalisation guard for the seed loop: it iterates
// coreagent.SystemAgents(), so any System Agent that declares a default soul
// via coreagent.SystemAgentDefaultSoul gets it on disk with no further edit to
// pkg/gateway. A future agent whose soul silently never lands fails here.
func TestSeedSystemAgentEagerSouls_SeedsEverySystemAgentWithADefaultSoul(t *testing.T) {
	api := newSeededJudgeAPI(t)
	cfg := api.agentLoop.GetConfig()

	for _, sa := range coreagent.SystemAgents() {
		want := coreagent.SystemAgentDefaultSoul(sa.ID)
		if strings.TrimSpace(want) == "" {
			continue // no compiled default soul — nothing to backfill
		}
		ws, wsErr := agentWorkspacePath(cfg, string(sa.ID), "", api.homePath)
		require.NoError(t, wsErr, "resolve workspace for %s", sa.ID)
		got, readErr := os.ReadFile(filepath.Join(ws, "SOUL.md"))
		require.NoErrorf(t, readErr,
			"System Agent %s declares a default soul but none reached disk at boot", sa.ID)
		assert.Equalf(t, want, string(got),
			"System Agent %s's on-disk soul must be its compiled default", sa.ID)
	}
}

// --- Bug 1: a single corrupt core-agent entity record must not abort boot ---

// TestPersistSeededCoreAgents_CorruptRecordDoesNotAbortBoot proves the fix:
// when one seeded core agent's entity record on disk is corrupt/unparseable,
// persistSeededCoreAgents skips re-seeding THAT agent (logs and continues)
// instead of returning an error that would abort the whole boot sequence.
// The historical behavior treated store.Get's parse error identically to
// "does not exist", driving a store.Create that failed with
// entity.ErrAlreadyExists (the file DOES exist, it just didn't parse) — one
// corrupt file made the entire gateway unbootable.
func TestPersistSeededCoreAgents_CorruptRecordDoesNotAbortBoot(t *testing.T) {
	home := t.TempDir()
	entitiesDir := filepath.Join(home, "entities", "agents")
	require.NoError(t, os.MkdirAll(entitiesDir, 0o700))

	// mia.json exists but is corrupt (not valid JSON) — simulates a
	// truncated write, disk corruption, or a hand-edit gone wrong.
	corruptPath := filepath.Join(entitiesDir, "mia.json")
	corruptBytes := []byte("{not valid json at all")
	require.NoError(t, os.WriteFile(corruptPath, corruptBytes, 0o600))

	seeded := []config.AgentConfig{
		{ID: "mia", Name: "Mia"},
		{ID: "jim", Name: "Jim"}, // a normal, brand-new core agent alongside it
	}

	err := persistSeededCoreAgents(home, seeded)
	require.NoError(t, err, "a single corrupt entity record must not abort boot")

	// The corrupt file must be left exactly as it was — no clobbering
	// attempt, no silent overwrite.
	after, readErr := os.ReadFile(corruptPath)
	require.NoError(t, readErr)
	assert.Equal(t, corruptBytes, after, "corrupt record must be left untouched, not overwritten")

	// The healthy sibling agent must still have been created normally.
	jimCfg, getErr := agentstore.New(home).Get("jim")
	require.NoError(t, getErr, "a healthy sibling agent must still persist normally")
	assert.Equal(t, "jim", jimCfg.ID)
}

// TestPersistSeededCoreAgents_NotFoundCreatesNormally is a negative control
// proving the ErrNotFound path (genuinely new agent, no file yet) still
// creates the record — the fix didn't turn EVERY Get error into a silent
// skip, only the non-ErrNotFound ones.
func TestPersistSeededCoreAgents_NotFoundCreatesNormally(t *testing.T) {
	home := t.TempDir()
	err := persistSeededCoreAgents(home, []config.AgentConfig{{ID: "ava", Name: "Ava"}})
	require.NoError(t, err)

	got, getErr := agentstore.New(home).Get("ava")
	require.NoError(t, getErr)
	assert.Equal(t, "ava", got.ID)
}

// --- Bug 2 / verified privilege-escalation fix: strict roster population ---

// TestPopulateAgentsListFromEntityStoreStrict_GenuineListErrorRejectsAndPreservesRoster
// proves a genuine entity.Store.List() failure (here: entities/agents/
// shadowed by a regular file, so os.ReadDir returns ENOTDIR — NOT
// os.IsNotExist) is propagated as an error, and cfg.Agents.List is left
// completely untouched rather than silently emptied. This is the exact class
// the legacy log-and-return behavior mishandled: a transient EMFILE/EACCES/
// EIO List() failure looked identical to "nothing configured yet".
func TestPopulateAgentsListFromEntityStoreStrict_GenuineListErrorRejectsAndPreservesRoster(t *testing.T) {
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "entities"), 0o700))
	// Shadow entities/agents with a FILE, not a directory, so os.ReadDir
	// fails with a genuine (non-NotExist) error regardless of process UID
	// (unlike an EACCES-via-chmod approach, which is a no-op for root).
	require.NoError(t, os.WriteFile(filepath.Join(home, "entities", "agents"), []byte("not a directory"), 0o600))

	preexisting := []config.AgentConfig{{ID: "sentinel-preexisting", Name: "should survive"}}
	cfg := &config.Config{Agents: config.AgentsConfig{List: append([]config.AgentConfig{}, preexisting...)}}

	err := populateAgentsListFromEntityStoreStrict(cfg, home)
	require.Error(t, err, "a genuine List() failure must be reported, not swallowed")
	assert.Equal(t, preexisting, cfg.Agents.List,
		"on a genuine store failure, cfg.Agents.List must be left exactly as it was — never silently emptied")
}

// TestPopulateAgentsListFromEntityStoreStrict_AllRecordsUnparseableRejected
// proves the roster-emptiness invariant: when on-disk agent records exist
// but EVERY one of them fails to parse (List() succeeds with err == nil, but
// agents comes back empty and skipped covers every id — e.g. a breaking
// schema change), that must be rejected as a hard failure, never silently
// accepted as "fresh install, zero agents".
func TestPopulateAgentsListFromEntityStoreStrict_AllRecordsUnparseableRejected(t *testing.T) {
	home := t.TempDir()
	entitiesDir := filepath.Join(home, "entities", "agents")
	require.NoError(t, os.MkdirAll(entitiesDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(entitiesDir, "mia.json"), []byte("{bad"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(entitiesDir, "jim.json"), []byte("also bad"), 0o600))

	cfg := &config.Config{}
	err := populateAgentsListFromEntityStoreStrict(cfg, home)
	require.Error(t, err, "every on-disk record failing to parse must be a hard failure")
	assert.Empty(t, cfg.Agents.List, "cfg.Agents.List must stay untouched (was already empty here)")
}

// TestPopulateAgentsListFromEntityStoreStrict_FreshInstallIsNotAnError is the
// negative control for the invariant above: a genuinely fresh install (no
// entities/agents/ directory at all yet) must NOT be treated as a failure —
// entity.Store.List() maps a missing directory to (nil, nil, nil).
func TestPopulateAgentsListFromEntityStoreStrict_FreshInstallIsNotAnError(t *testing.T) {
	home := t.TempDir() // entities/agents/ never created
	cfg := &config.Config{}
	err := populateAgentsListFromEntityStoreStrict(cfg, home)
	require.NoError(t, err, "a genuinely fresh install (zero on-disk records) must not error")
	assert.Empty(t, cfg.Agents.List)
}

// TestPopulateAgentsListFromEntityStoreStrict_RegressionGuardRejectsEmptyAfterNonEmpty
// proves the same-process regression guard (the BUG2 defense): a previously
// non-empty roster observed for this home going empty on a later call — e.g.
// because homePath momentarily resolved to the wrong directory — is
// rejected rather than silently wiping the live roster.
func TestPopulateAgentsListFromEntityStoreStrict_RegressionGuardRejectsEmptyAfterNonEmpty(t *testing.T) {
	home := t.TempDir()
	store := agentstore.New(home)
	seed := config.AgentConfig{ID: "mia", Name: "Mia"}
	require.NoError(t, store.Create("mia", &seed))

	firstCfg := &config.Config{}
	require.NoError(t, populateAgentsListFromEntityStoreStrict(firstCfg, home))
	require.Len(t, firstCfg.Agents.List, 1, "first call should observe the real, non-empty roster")

	// Now simulate the roster disappearing for this SAME home (e.g. the
	// directory was wiped, or — the real-world BUG2 case — homePath
	// resolution glitched to an empty sibling directory momentarily).
	require.NoError(t, os.RemoveAll(store.Dir()))

	secondCfg := &config.Config{}
	err := populateAgentsListFromEntityStoreStrict(secondCfg, home)
	require.Error(t, err, "a non-empty roster going empty for the same home must be rejected")
	assert.Empty(t, secondCfg.Agents.List, "the fresh (never-populated) cfg for this failed call must stay untouched")
}

// TestPersistSeededSkillGrants_WriteOnceThenByteIdenticalNoOp verifies the
// marker lands in config.json exactly once: the first call writes it, the
// second call (same markers — the second-boot shape) leaves the file
// byte-identical and untouched (spec test 16's "second boot byte-identical",
// at the file level).
func TestPersistSeededSkillGrants_WriteOnceThenByteIdenticalNoOp(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	require.NoError(t, os.WriteFile(configPath,
		[]byte(`{"version":1,"agents":{"defaults":{}},"providers":[]}`), 0o600))

	markers := []string{coreagent.SkillsMigrationDefineDone}
	require.NoError(t, persistSeededSkillGrants(configPath, markers))

	afterFirst, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(afterFirst, &m))
	assert.Equal(t, []any{coreagent.SkillsMigrationDefineDone}, m["seeded_skill_grants"],
		"first persist must write the marker into config.json")
	assert.Equal(t, float64(1), m["version"], "every other key must be preserved as-is")

	// Second boot: same markers → no write at all, file byte-identical.
	require.NoError(t, persistSeededSkillGrants(configPath, markers))
	afterSecond, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Equal(t, afterFirst, afterSecond,
		"a second persist with identical markers must leave config.json byte-identical")
}
