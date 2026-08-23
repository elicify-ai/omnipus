// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// Integration tests for the retro-retention sweep (FR-034).
//
// Covers:
//   - TestIntegration_RetroSweep180Split — 190-day retro is deleted, 170-day
//     survives (180-day cutoff), 100-day session is swept (90-day cutoff).
//     Validates the 90/180 split in a single run.
//   - TestIntegration_DefaultRetroRetentionIs180 — RetentionMemoryRetrosDays()
//     defaults to 180 when unset (regression guard); session default stays 90.
//
// These tests call SweepRetros and RetentionSweep at their function boundaries
// because there is no on-demand REST endpoint for the retro sweep (see testability
// note at the bottom of this file).  executeRetroSweep is also exercised
// through the package-level function to cover the full agent-iteration path.

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

// retroIT_newMemoryStore builds a MemoryStore backed by a temp directory and
// injects a fixed clock so retro-directory name comparisons are deterministic.
// agentWorkspace is the root under which ".omnipus/retros/YYYY-MM-DD/" lives.
func retroIT_newMemoryStore(t *testing.T, agentWorkspace string, now time.Time) *agent.MemoryStore {
	t.Helper()
	ms := agent.NewMemoryStore(agentWorkspace, agentWorkspace)
	ms.SetClock(func() time.Time { return now })
	return ms
}

// retroIT_createRetroDir creates the retro directory structure for a given date
// and writes a minimal *_retro.md file inside it so SweepRetros has something
// to delete.
//
// Structure: <privateRoot>/retros/<date>/<sessionID>_retro.md
// privateRoot is <agentWorkspace>/.omnipus
func retroIT_createRetroDir(t *testing.T, agentWorkspace string, date time.Time, sessionID string) string {
	t.Helper()
	dateStr := date.UTC().Format("2006-01-02")
	privateRoot := filepath.Join(agentWorkspace, ".omnipus")
	retroDir := filepath.Join(privateRoot, "retros", dateStr)
	require.NoError(t, os.MkdirAll(retroDir, 0o700), "create retro dir %s", retroDir)
	retroFile := filepath.Join(retroDir, sessionID+"_retro.md")
	content := "<!-- ts=2026-01-01T00:00:00.000Z trigger=manual fallback=false -->\n## Session recap\ntest\n### Went well\n- ok\n### Needs improvement\n- nothing\n<!-- next -->\n"
	require.NoError(t, os.WriteFile(retroFile, []byte(content), 0o600), "write retro file %s", retroFile)
	return retroFile
}

// retroIT_createSessionFile writes a backdated .jsonl file inside a session
// subdirectory of baseDir.  mtime is set to simulate age (used by RetentionSweep).
func retroIT_createSessionFile(t *testing.T, baseDir, sessionID, filename string, age time.Duration) string {
	t.Helper()
	sessionDir := filepath.Join(baseDir, sessionID)
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	filePath := filepath.Join(sessionDir, filename)
	require.NoError(t, os.WriteFile(filePath, []byte(`{"id":"test"}`+"\n"), 0o600))
	mtime := time.Now().Add(-age)
	require.NoError(t, os.Chtimes(filePath, mtime, mtime))
	return filePath
}

// ─── TestIntegration_RetroSweep180Split ──────────────────────────────────────

// TestIntegration_RetroSweep180Split verifies the 90/180 retention split.
//
// BDD:
//
//	Given a MemoryStore with a fixed clock anchored at "now"
//	And a retro file in directory dated 190 days ago (older than 180-day cutoff)
//	And a retro file in directory dated 170 days ago (newer than 180-day cutoff)
//	And a session .jsonl file with mtime 100 days ago (older than 90-day cutoff)
//	When SweepRetros(180) is called on the MemoryStore
//	And RetentionSweep(90) is called on the UnifiedStore
//	Then the 190-day retro file is DELETED
//	And the 170-day retro file SURVIVES
//	And the 100-day session file is DELETED
//	And the default (RetentionMemoryRetrosDays on zero config) is 180
//
// This test also exercises executeRetroSweep to validate the end-to-end
// agent-iteration path.
func TestIntegration_RetroSweep180Split(t *testing.T) {
	// ── Arrange ──────────────────────────────────────────────────────────────

	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)

	agentWorkspace := t.TempDir()
	ms := retroIT_newMemoryStore(t, agentWorkspace, now)

	// Retro dated 190 days ago: should be DELETED by SweepRetros(180)
	date190 := now.AddDate(0, 0, -190)
	staleFile := retroIT_createRetroDir(t, agentWorkspace, date190, "sess-stale")

	// Retro dated 170 days ago: should SURVIVE SweepRetros(180)
	date170 := now.AddDate(0, 0, -170)
	freshFile := retroIT_createRetroDir(t, agentWorkspace, date170, "sess-fresh")

	// Session store for the 90-day sweep half.
	sessDir := t.TempDir()
	sessStore, err := session.NewUnifiedStore(sessDir)
	require.NoError(t, err, "NewUnifiedStore must succeed")

	// Session file aged 100 days: should be DELETED by RetentionSweep(90)
	staleSession := retroIT_createSessionFile(t, sessDir, "sess-aged", "2026-01-01.jsonl", 100*24*time.Hour)

	// ── Act: retro sweep at the 180-day boundary ──────────────────────────────

	deleted, err := ms.SweepRetros(180)
	require.NoError(t, err, "SweepRetros must not return an error")

	// ── Assert: retro split ───────────────────────────────────────────────────

	assert.Equal(t, 1, deleted,
		"SweepRetros(180): exactly 1 retro file should be deleted (the 190-day-old one)")

	_, err = os.Stat(staleFile)
	assert.True(t, os.IsNotExist(err),
		"190-day-old retro file must be deleted by SweepRetros(180); stat=%v", err)

	_, err = os.Stat(freshFile)
	assert.NoError(t, err,
		"170-day-old retro file must survive SweepRetros(180)")

	// Differentiation check: a second, DIFFERENT input (retentionDays=200)
	// should leave BOTH files alive on a freshly-created store.
	// This catches any hardcoded-deletion behavior.
	agentWorkspace2 := t.TempDir()
	ms2 := retroIT_newMemoryStore(t, agentWorkspace2, now)
	retroIT_createRetroDir(t, agentWorkspace2, date190, "sess-stale2")
	retroIT_createRetroDir(t, agentWorkspace2, date170, "sess-fresh2")

	deleted2, err := ms2.SweepRetros(200)
	require.NoError(t, err, "SweepRetros(200) must not error")
	assert.Equal(t, 0, deleted2,
		"SweepRetros(200): both files are younger than 200 days, nothing should be deleted")

	// ── Act: session sweep at the 90-day boundary ────────────────────────────

	removedSess, err := sessStore.RetentionSweep(90)
	require.NoError(t, err, "RetentionSweep(90) must not error")

	// ── Assert: session split ─────────────────────────────────────────────────

	assert.Equal(t, 1, removedSess,
		"RetentionSweep(90): the 100-day-old session file must be deleted")

	_, err = os.Stat(staleSession)
	assert.True(t, os.IsNotExist(err),
		"100-day-old session .jsonl must be swept by RetentionSweep(90); stat=%v", err)

	// ── Assert: default resolves to 180 ──────────────────────────────────────

	zeroCfg := config.OmnipusRetentionConfig{} // MemoryRetrosDays unset
	assert.Equal(t, 180, zeroCfg.RetentionMemoryRetrosDays(),
		"default RetentionMemoryRetrosDays must be 180 when MemoryRetrosDays is unset")

	// ── Act: executeRetroSweep path (end-to-end agent iteration) ─────────────
	// Build a minimal AgentLoop with one real, chat-target agent ("mia") wired
	// to our MemoryStore. This exercises executeRetroSweep →
	// registry.ListAgentIDs() → SweepRetros.
	//
	// We do this by:
	//  1. Building the loop with "mia" seeded into cfg.Agents.List. The retired
	//     "main" sentinel used to auto-register regardless of cfg and share
	//     AgentDefaults.Home directly (the "unidentified agent" case in
	//     pkg/agent/instance.go's resolveAgentHome); it is gone with no
	//     back-compat, and a REAL named agent like "mia" gets its own
	//     per-agent workspace under $OMNIPUS_HOME/agents/mia/ instead — so the
	//     backdated retro below is planted under mia's ACTUAL resolved Home,
	//     not the AgentDefaults.Home value.
	//  2. Patching retentionRetroSweepFn to call executeRetroSweep with our loop.
	//  3. Asserting executeRetroSweep deletes the planted stale retro; this
	//     verifies the iteration path runs without panic and honors a
	//     retentionDays boundary for each registered agent.
	tmpHome := t.TempDir()
	loopCfg := &config.Config{
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Home:         tmpHome,
				DefaultModel: config.DefaultModel{Model: "test-model"},
				MaxTokens:    4096,
			},
			List: []config.AgentConfig{{ID: "mia"}},
		},
	}
	msgBus := bus.NewMessageBus()
	al := mustAgentLoop(t, loopCfg, msgBus, &restMockProvider{})

	// Resolve mia's REAL workspace (not tmpHome — see the comment above) before
	// planting the backdated retro so the sweep actually has work to do.
	registry := al.GetRegistry()
	miaInst, ok := registry.GetAgent("mia")
	require.True(t, ok, "AgentLoop must have 'mia' agent registered")
	require.NotNil(t, miaInst.ContextBuilder, "'mia' agent must have a ContextBuilder")

	miaPrivate := filepath.Join(miaInst.Home, ".omnipus", "retros")
	staleDate := now.AddDate(0, 0, -200).UTC().Format("2006-01-02")
	staleDir := filepath.Join(miaPrivate, staleDate)
	require.NoError(t, os.MkdirAll(staleDir, 0o700))
	require.NoError(t, os.WriteFile(
		filepath.Join(staleDir, "sess-integration_retro.md"),
		[]byte(
			"<!-- ts=2025-12-08T00:00:00.000Z trigger=manual fallback=false -->\n## Session recap\ntest\n<!-- next -->\n",
		),
		0o600,
	))

	// Inject a clock into mia's MemoryStore.
	miaMemory := miaInst.ContextBuilder.Memory()
	require.NotNil(t, miaMemory, "'mia' agent's ContextBuilder.Memory() must not be nil")
	miaMemory.SetClock(func() time.Time { return now })

	// executeRetroSweep is in the same package (gateway); call it directly.
	totalDeleted := executeRetroSweep(al, 180)
	assert.GreaterOrEqual(t, totalDeleted, 1,
		"executeRetroSweep with 180-day retention must delete the 200-day-old retro file")
}

// ─── TestIntegration_DefaultRetroRetentionIs180 ──────────────────────────────

// TestIntegration_DefaultRetroRetentionIs180 is a focused regression guard that
// verifies:
//   - RetentionMemoryRetrosDays() returns 180 when MemoryRetrosDays is 0
//     (the new default introduced to outlive the 90-day session window).
//   - RetentionSessionDays() returns 90 when SessionDays is 0.
//   - A SweepRetros call with retentionDays=0 (unset) resolves to 180 and
//     deletes a 181-day-old retro file while leaving a 179-day-old file intact.
//
// BDD:
//
//	Given a config with MemoryRetrosDays unset (zero value)
//	When RetentionMemoryRetrosDays() is called
//	Then it returns 180
//
//	Given a config with SessionDays unset (zero value)
//	When RetentionSessionDays() is called
//	Then it returns 90
//
//	Given a MemoryStore with a fixed clock
//	And retro files at 181 and 179 days ago
//	When SweepRetros is called with the default 180-day retention
//	Then the 181-day retro is deleted and the 179-day retro survives
func TestIntegration_DefaultRetroRetentionIs180(t *testing.T) {
	// ── Default value assertions ──────────────────────────────────────────────

	zeroCfg := config.OmnipusRetentionConfig{}
	assert.Equal(t, 180, zeroCfg.RetentionMemoryRetrosDays(),
		"RetentionMemoryRetrosDays must default to 180 (prevents regression if default changes)")

	assert.Equal(t, 90, zeroCfg.RetentionSessionDays(),
		"RetentionSessionDays must default to 90 (the 90-day session window)")

	assert.NotEqual(t, zeroCfg.RetentionMemoryRetrosDays(), zeroCfg.RetentionSessionDays(),
		"retro default (180) must differ from session default (90) — retros outlive transcripts")

	// ── Real sweep at the 180-day boundary ───────────────────────────────────

	now := time.Date(2026, 6, 26, 12, 0, 0, 0, time.UTC)
	agentWorkspace := t.TempDir()
	ms := retroIT_newMemoryStore(t, agentWorkspace, now)

	// 181 days ago: older than 180 → DELETED
	stale := retroIT_createRetroDir(t, agentWorkspace, now.AddDate(0, 0, -181), "sess-stale-default")

	// 179 days ago: newer than 180 → SURVIVES
	fresh := retroIT_createRetroDir(t, agentWorkspace, now.AddDate(0, 0, -179), "sess-fresh-default")

	retentionDays := zeroCfg.RetentionMemoryRetrosDays() // 180
	deleted, err := ms.SweepRetros(retentionDays)
	require.NoError(t, err, "SweepRetros(%d) must not error", retentionDays)

	assert.Equal(t, 1, deleted,
		"SweepRetros(%d): exactly the 181-day-old file must be deleted", retentionDays)

	_, err = os.Stat(stale)
	assert.True(t, os.IsNotExist(err),
		"181-day-old retro file must be deleted by SweepRetros(%d)", retentionDays)

	_, err = os.Stat(fresh)
	assert.NoError(t, err,
		"179-day-old retro file must survive SweepRetros(%d)", retentionDays)

	// ── Verify IsDisabled is false by default ─────────────────────────────────

	assert.False(t, zeroCfg.IsDisabled(),
		"retention must not be disabled by default — IsDisabled() on zero OmnipusRetentionConfig must be false")

	// ── Explicit custom value overrides the default ───────────────────────────

	customCfg := config.OmnipusRetentionConfig{MemoryRetrosDays: 365}
	assert.Equal(t, 365, customCfg.RetentionMemoryRetrosDays(),
		"explicit MemoryRetrosDays=365 must be returned as-is (default not applied when set)")

	customSessionCfg := config.OmnipusRetentionConfig{SessionDays: 30}
	assert.Equal(t, 30, customSessionCfg.RetentionSessionDays(),
		"explicit SessionDays=30 must be returned as-is")
}

// ─── Testability note ─────────────────────────────────────────────────────────
//
// The retro sweep currently has NO on-demand REST endpoint (unlike the session
// sweep which has POST /api/v1/security/retention/sweep).  Tests must therefore
// either:
//
//  (a) Call SweepRetros directly on a MemoryStore (used above for the cutoff-math
//      integration level), or
//  (b) Call executeRetroSweep with a real AgentLoop (used above for end-to-end
//      agent-iteration coverage).
//
// A dedicated POST /api/v1/security/retention/retro-sweep endpoint would
// significantly improve testability and operator ergonomics (one-shot purge
// without waiting for the nightly tick).  The session-sweep endpoint at
// pkg/gateway/rest.go::HandleRetentionSweep is the template.  Tracking issue
// should be opened against v0.2/v0.3 — this is a testability gap flagged by
// the reviewers.
