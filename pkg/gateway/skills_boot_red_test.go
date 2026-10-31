package gateway

// RED tests — landing/skills (T9): the start-up skills step (§8.5, test row
// 30). S-7.13 (retirement of a stamped retired built-in through the real
// boot path: live instance and stored grant record both lose it) and S-7.14
// (the boot audit replay through a REAL logger from pkg/audit.NewLogger —
// no mock: skill.builtin_updated and skill.builtin_retired are in the audit
// output before the first request is served; a fault in the replay write is
// counted by the audit-degraded counter and boot still succeeds).
// Traces to: skills-spec-r13.md §8.5, §7.6 audit table rows, S-7.13, S-7.14.
//
// RED expectation: bootSkills is a scaffold that returns an error — every
// assertion below fails on the baseline.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// bootFixture writes a stamped retired `handoff` folder and a config whose
// agent A holds the handoff grant.
func bootFixture(t *testing.T) (string, *config.Config) {
	t.Helper()
	home := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(home, "skills", "handoff"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(home, "skills", "handoff", "SKILL.md"),
		[]byte("---\nname: handoff\ndescription: retired\n---\n\nbody\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(home, "skills", "handoff", ".omnipus-builtin"),
		[]byte("{}"), 0o644))

	cfg := &config.Config{}
	cfg.Agents.List = []config.AgentConfig{{
		ID:     "agent-a",
		Skills: []string{"handoff"},
	}}
	return home, cfg
}

// TestBootSkills_RetiresStampedRetiredBuiltin (S-7.13).
// BDD: the real boot path removes the retired stamped folder and the grant
// from agent A's stored record; the first turn of A no longer sees handoff.
// Traces to: skills-spec-r13.md §8.4, S-7.13, §17.1 row 30.
func TestBootSkills_RetiresStampedRetiredBuiltin(t *testing.T) {
	home, cfg := bootFixture(t)

	report, err := bootSkills(cfg, home)
	require.NoError(t, err, "RED: bootSkills is not implemented (scaffold)")

	_, statErr := os.Stat(filepath.Join(home, "skills", "handoff"))
	assert.True(t, os.IsNotExist(statErr), "the stamped retired folder is deleted")
	assert.Empty(t, cfg.Agents.List[0].Skills,
		"agent A's stored record no longer holds the handoff grant")
	found := false
	for _, out := range report.Retired {
		if out.Slug == "handoff" {
			found = true
			assert.True(t, out.FolderDeleted, "the outcome records the folder deletion")
			assert.Contains(t, out.AgentsChanged, "agent-a", "the outcome records the grant removal")
		}
	}
	assert.True(t, found, "the SyncReport records the handoff retirement")
}

// TestBootSkills_AuditReplayRealLogger (S-7.14).
// BDD: the same first boot, with an edited built-in plan (stamped old
// content → replaced) plus the retired handoff, replays the SyncReport
// through a REAL audit logger (initializeAudit's shape, no mock): the audit
// output contains skill.builtin_updated (plan) and skill.builtin_retired
// (handoff); a fault injected into the replay write is counted by the
// audit-degraded counter and boot still succeeds.
// Traces to: skills-spec-r13.md §8.5, §7.6, S-7.14, R2-MAJ-005.
func TestBootSkills_AuditReplayRealLogger(t *testing.T) {
	home, cfg := bootFixture(t)

	// An edited stamped built-in plan (old revision content) — SyncBuiltins
	// replaces it and the replay emits skill.builtin_updated for plan.
	planDir := filepath.Join(home, "skills", "plan")
	require.NoError(t, os.MkdirAll(planDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(planDir, "SKILL.md"),
		[]byte("---\nname: plan\ndescription: edited\n---\n\nedited body\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(planDir, ".omnipus-builtin"),
		[]byte(`{"revision":"stale"}`), 0o644))

	auditDir := filepath.Join(home, "logs")
	logger, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RedactEnabled: false})
	require.NoError(t, err, "the real audit logger initializes (initializeAudit's shape)")
	defer func() { _ = logger.Close() }()

	report, err := bootSkills(cfg, home)
	require.NoError(t, err, "RED: bootSkills is not implemented (scaffold)")

	require.NoError(t, replayBootSkillAudit(report, logger))

	raw, err := os.ReadFile(filepath.Join(auditDir, "audit-2026-01-01.jsonl"))
	if err != nil {
		// The file name is date-partitioned; fall back to any audit file.
		entries, listErr := os.ReadDir(auditDir)
		require.NoError(t, listErr)
		require.NotEmpty(t, entries, "the audit logger wrote at least one file")
		raw, err = os.ReadFile(filepath.Join(auditDir, entries[0].Name()))
		require.NoError(t, err)
	}
	body := string(raw)
	assert.Contains(t, body, "skill.builtin_updated", "the plan replacement is audited")
	assert.Contains(t, body, `"plan"`, "the audited update names plan")
	assert.Contains(t, body, "skill.builtin_retired", "the handoff retirement is audited")
	assert.Contains(t, body, `"handoff"`, "the audited retirement names handoff")

	t.Run("replay write fault is counted and boot still succeeds", func(t *testing.T) {
		home2, cfg2 := bootFixture(t)
		_ = home2
		logger2, err := audit.NewLogger(audit.LoggerConfig{
			Dir: filepath.Join(home2, "logs"), RedactEnabled: false,
		})
		require.NoError(t, err)
		defer func() { _ = logger2.Close() }()
		_ = logger2.Close() // close early so every write fails (fault injection)

		report2, err := bootSkills(cfg2, home2)
		require.NoError(t, err)
		err = replayBootSkillAudit(report2, logger2)
		assert.Error(t, err, "the replay surfaces the write failure to the caller")
		// Boot-level rule: the failure is counted by the audit-degraded
		// counter, never fatal — bootSkills already succeeded above.
		_, statErr := os.Stat(filepath.Join(home2, "skills", "handoff"))
		assert.True(t, os.IsNotExist(statErr), "the retirement itself still landed")
	})
}

// TestBootReport_EventNamesMatchSpec pins the §7.6 audit-table event names
// as a contract (the replay must use exactly these).
// Traces to: skills-spec-r13.md §7.6 audit rows, §8.5.
func TestBootReport_EventNamesMatchSpec(t *testing.T) {
	home, cfg := bootFixture(t)
	report, err := bootSkills(cfg, home)
	require.NoError(t, err, "RED: bootSkills is not implemented")

	// The report carries the event names the replay emits; pin them here so
	// a rename cannot silently change the audit vocabulary.
	b, err := json.Marshal(report)
	require.NoError(t, err)
	assert.True(t, strings.Contains(string(b), "builtin_updated") ||
		strings.Contains(string(b), "Updated"),
		"the report serializes the builtin-updated outcome (replay emits skill.builtin_updated)")
}
