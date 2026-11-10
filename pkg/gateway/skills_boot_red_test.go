package gateway

// RED reconciliation — r15 §8.5, S-7.13/S-7.14, CHECK-09/CHECK-11.
// Helper tests and actual startup wiring are separate claims. RetireBuiltins
// still removes product-retired grants; SD-30 removes deletion-dependent
// correctness sweeps. Answered Q-S3 (2026-10-31) separately requires best-effort
// managed-deletion hygiene, never an authorization dependency.
import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/agentstore"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/tools"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func bootFixture(t *testing.T) (string, *config.Config) {
	t.Helper()
	home := t.TempDir()
	t.Setenv(config.EnvHome, home)
	t.Setenv("HOME", t.TempDir())
	t.Setenv("OMNIPUS_BEARER_TOKEN", "")
	t.Setenv("OMNIPUS_MASTER_KEY", "0000000000000000000000000000000000000000000000000000000000000000")
	dir := filepath.Join(home, "skills", "handoff")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: handoff\ndescription: retired\n---\nbody\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-builtin"), []byte("{}"), 0o644))
	cfg := config.DefaultConfig()
	cfg.Agents.Defaults.Home = filepath.Join(home, "agents")
	cfg.Agents.List = []config.AgentConfig{{ID: "agent-a", Name: "Agent A", Skills: []string{"handoff"}}}
	require.NoError(t, agentstore.New(home).Create("agent-a", &cfg.Agents.List[0]))
	require.NoError(t, config.SaveConfig(filepath.Join(home, "config.json"), cfg))
	old := skillsBootReport
	t.Cleanup(func() { skillsBootReport = old })
	return home, cfg
}

func TestBootSkills_RetiresStampedRetiredBuiltin(t *testing.T) {
	home, cfg := bootFixture(t)
	report, err := bootSkills(cfg, home)
	require.NoError(t, err, "RED: skills startup helper is not implemented")
	_, err = os.Stat(filepath.Join(home, "skills", "handoff"))
	assert.True(t, errors.Is(err, os.ErrNotExist))
	durable, err := agentstore.New(home).Get("agent-a")
	require.NoError(t, err)
	assert.NotContains(t, durable.Skills, "handoff", "the disk record, not only config cache, loses the retired grant")
	assert.NotContains(t, cfg.Agents.List[0].Skills, "handoff")
	found := false
	for _, out := range report.Retired {
		if out.Slug == "handoff" {
			found = true
			assert.True(t, out.FolderDeleted)
			assert.Equal(t, []string{"agent-a"}, out.AgentsChanged)
		}
	}
	assert.True(t, found, "report contains actual retirement")
}

// This drives the production entry point. A working but uncalled bootSkills
// helper cannot make the test green. No network/model request is made.
func TestBootSkills_ActualStartupBeforeFirstLiveAgent(t *testing.T) {
	home, _ := bootFixture(t)
	bootEditedPlan(t, home)
	rc := &runContextWithOptions{ctx: context.Background(), homePath: home, configPath: filepath.Join(home, "config.json"), allowEmptyStartup: true}
	err, stop := rc.loadConfigAndProvider()
	if rc.credStore != nil {
		t.Cleanup(rc.credStore.Close)
	}
	require.NoError(t, err, "real credentials/config/roster startup must succeed")
	require.False(t, stop)
	durable, err := agentstore.New(home).Get("agent-a")
	require.NoError(t, err)
	require.NotContains(t, durable.Skills, "handoff", "real startup must retire before constructing an agent")
	// The model transport is the process edge, replaced only AFTER the real
	// startup sequence. No helper manually invokes bootSkills or audit replay.
	rc.provider = &restMockProvider{}
	err, stop = rc.initializeAgentLoop()
	if rc.agentLoop != nil {
		t.Cleanup(rc.agentLoop.Close)
	}
	if rc.liveLimits != nil {
		t.Cleanup(rc.liveLimits.Close)
		t.Cleanup(func() { agent.SetLiveWindowLookup(nil) })
	}
	// TempDir-cleanup family (#1644/#1695): initializeAgentLoop started the
	// boot-stage writer goroutines (browser cache-trim schedule,
	// managed-Chromium preprovision download, unknown-window primer) and
	// nothing joined them — the preprovision download was measured still
	// writing browser/chromium/*.zip.part-* into this test's home AFTER the
	// whole cleanup sequence above had run. Registered LAST so LIFO runs it
	// FIRST, before the agent-loop close and the TempDir removal bootFixture's
	// t.TempDir registered.
	stopBootWritersOnCleanup(t, rc)
	require.NoError(t, err)
	require.False(t, stop)
	inst, ok := rc.agentLoop.GetRegistry().GetAgent("agent-a")
	require.True(t, ok)
	assert.NotContains(t, inst.SkillsFilter, "handoff", "first actual agent instance has no retired grant")
	skillTool, ok := inst.Tools.Get("Skill")
	require.True(t, ok, "reachability control: actual Skill tool is registered")
	ctx := tools.WithAgentID(context.Background(), "agent-a")
	res := skillTool.Execute(ctx, map[string]any{"name": "handoff"})
	assert.True(t, res.IsError, "first instance cannot activate the retired skill")
	// Replay must have happened at actual startup's existing audit-logger
	// wiring point; no helper is called manually to manufacture these events.
	logger := rc.agentLoop.AuditLogger()
	require.NotNil(t, logger)
	require.NoError(t, logger.Close())
	updated, retired := false, false
	for _, row := range bootAuditRows(t, filepath.Join(home, "system")) {
		if row.Event == "skill.builtin_updated" && row.Details["slug"] == "plan" {
			updated = true
		}
		if row.Event == "skill.builtin_retired" && row.Details["slug"] == "handoff" {
			retired = true
		}
	}
	assert.True(t, updated, "real boot automatically replays the changed plan event")
	assert.True(t, retired, "real boot automatically replays the retired handoff event")
}

func bootEditedPlan(t *testing.T, home string) {
	t.Helper()
	dir := filepath.Join(home, "skills", "plan")
	require.NoError(t, os.MkdirAll(dir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("---\nname: plan\ndescription: edited\n---\nedited body\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, ".omnipus-builtin"), []byte(`{"revision":"stale"}`), 0o644))
}

func bootAuditRows(t *testing.T, dir string) []audit.Entry {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	require.NoError(t, err)
	require.NotEmpty(t, paths, "real audit output must exist")
	rows := []audit.Entry{}
	for _, p := range paths {
		f, err := os.Open(p)
		require.NoError(t, err)
		scan := bufio.NewScanner(f)
		for scan.Scan() {
			var row audit.Entry
			require.NoError(t, json.Unmarshal(scan.Bytes(), &row))
			rows = append(rows, row)
		}
		require.NoError(t, scan.Err())
		require.NoError(t, f.Close())
	}
	require.NotEmpty(t, rows, "audit reader positive control")
	return rows
}

func TestBootSkills_AuditReplayRealLogger(t *testing.T) {
	home, cfg := bootFixture(t)
	bootEditedPlan(t, home)
	dir := filepath.Join(home, "logs")
	l, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RedactEnabled: false})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	report, err := bootSkills(cfg, home)
	require.NoError(t, err)
	require.NoError(t, replayBootSkillAudit(report, l))
	require.NoError(t, l.Close()) // flush and release before independent disk read
	foundUpdate, foundRetirement := false, false
	for _, row := range bootAuditRows(t, dir) {
		if row.Event == "skill.builtin_updated" && row.Details["slug"] == "plan" {
			foundUpdate = true
			assert.NotEmpty(t, row.Details["old_revision"])
			assert.NotEmpty(t, row.Details["new_revision"])
		}
		if row.Event == "skill.builtin_retired" && row.Details["slug"] == "handoff" {
			foundRetirement = true
			assert.Equal(t, true, row.Details["folder_deleted"])
			assert.Equal(t, []any{"agent-a"}, row.Details["agents_changed"])
		}
	}
	assert.True(t, foundUpdate, "actual plan update event with its own payload")
	assert.True(t, foundRetirement, "actual handoff retirement event with its own payload")
	t.Run("failed replay increments degraded count without undoing retirement", func(t *testing.T) {
		home, cfg := bootFixture(t)
		report, err := bootSkills(cfg, home)
		require.NoError(t, err)
		require.NotEmpty(t, report.Retired)
		l, err := audit.NewLogger(audit.LoggerConfig{Dir: filepath.Join(home, "logs"), RedactEnabled: false})
		require.NoError(t, err)
		require.NoError(t, l.Close())
		before := audit.SnapshotSkipped().Total
		err = replayBootSkillAudit(report, l)
		assert.Error(t, err)
		assert.Equal(t, before+int64(len(report.Updated)+len(report.Retired)), audit.SnapshotSkipped().Total, "one degraded emission per replay event, not a silent loss")
		row, err := agentstore.New(home).Get("agent-a")
		require.NoError(t, err)
		assert.NotContains(t, row.Skills, "handoff")
	})
}

func TestBootReport_EventNamesMatchSpec(t *testing.T) {
	home, cfg := bootFixture(t)
	bootEditedPlan(t, home)
	report, err := bootSkills(cfg, home)
	require.NoError(t, err)
	found := false
	for _, out := range report.Updated {
		if out.Slug == "plan" {
			found = true
			assert.NotEmpty(t, out.OldRev)
			assert.NotEmpty(t, out.NewRev)
			assert.NotEqual(t, out.OldRev, out.NewRev)
		}
	}
	require.True(t, found, "a real changed plan must yield a nonempty update, not a serialized empty field name")
	dir := filepath.Join(home, "report-audit")
	l, err := audit.NewLogger(audit.LoggerConfig{Dir: dir, RedactEnabled: false})
	require.NoError(t, err)
	t.Cleanup(func() { _ = l.Close() })
	require.NoError(t, replayBootSkillAudit(report, l))
	require.NoError(t, l.Close())
	updates := 0
	for _, row := range bootAuditRows(t, dir) {
		if row.Event == "skill.builtin_updated" && row.Details["slug"] == "plan" {
			updates++
		}
	}
	assert.Equal(t, 1, updates, "exactly one real event for the one updated built-in")
}
