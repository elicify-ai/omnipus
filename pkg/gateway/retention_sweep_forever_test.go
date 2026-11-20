// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

// retention_sweep_forever_test.go — Settings redesign #1519, RED.
//
// Test Implementation Order row 7 (G-14's evidence): the real sweep ticks
// with a controlled clock.
//
//   - TestRetroSweep_MemoryForeverKeepsRetros (FR-049, US-5 AS3: "Keep
//     forever stops memory deletion, through the real sweep") — and proves
//     forever stops ONLY the memory sweep: the chat transcript older than
//     session_days is still deleted while the reflection survives.
//   - TestRetroSweep_ForeverOffDeletes (the positive control: the same
//     fixture with forever off deletes the reflection, proving the
//     instrument can see a deletion).
//   - TestRetroSweep_ForeverOffResumes (US-5 "Turning Keep forever off again
//     resumes deletion": planted with forever on, saved off, the next tick
//     deletes the too-old reflection). The switch's description sentence is
//     a screen wording requirement (FR-049, second half) and is covered by
//     the dataRetention.section.test Vitest file, not here.
//   - TestRetroSweep_LegacyDisabledKeepsAll (FR-048: the legacy
//     storage.retention.disabled switch keeps stopping BOTH sweeps).
//
// Oracle: the spec's BDD scenarios "Keep forever stops memory deletion,
// through the real sweep", "The memory sweep deletes when Keep forever is
// off (positive control)", "Turning Keep forever off again resumes
// deletion", "The legacy keep-everything switch is shown, not hidden", and
// the C-3 stored-state table. RED today: memory_retros_forever does not
// exist, so the forever and resume cases cannot hold — the reflection is
// deleted with forever on (or the legacy case is the only one passing, as
// preserved behaviour).
//
// No sleeps: both tick functions are synchronous; when the call returns the
// sweep has completed (the completion signal), so every assertion below runs
// after a completed sweep.

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/session"
)

// sweepTestAgentID is the one configured agent of the harness (the shared
// gateway-test agent id).
const sweepTestAgentID = "01JXTESTAGENTSTARTTEST001"

// fixedSweepNow is the controlled clock: "now" as far as every sweep and
// every fixture date in this file is concerned.
var fixedSweepNow = time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)

// retentionSweepFixture holds the real stores one scenario runs its ticks against.
type retentionSweepFixture struct {
	cfg        *config.Config
	configPath string
	al         *agent.AgentLoop
	retroPath  string // the 200-day-old reflection
	transcript string // the 40-day-old chat transcript
	store      *session.UnifiedStore
}

// retentionDocF1 is storage.retention WITHOUT the new switch (valid at the
// baseline): legacy off, forever absent.
const retentionDocPlain = `"retention":{"session_days":30,"memory_retros_days":180}`

// retentionDocForever carries the new key (B6). The key is written into
// config.json as raw JSON: today the loader ignores it, so the ticks behave
// as if the switch were off — the RED statement. Once GREEN adds the field,
// the same doc loads into the struct and the same tests turn green, because
// the fixture's in-memory config always comes from a real config.LoadConfig
// of this file.
const retentionDocForever = `"retention":{"session_days":30,"memory_retros_days":180,"memory_retros_forever":true}`

// retentionDocLegacyDisabled is the legacy config-file-only keep-everything
// switch (FR-048), written the way an operator's config.json carries it.
const retentionDocLegacyDisabled = `"retention":{"session_days":30,"memory_retros_days":180,"disabled":true}`

// newRetentionSweepFixture plants a reflection dated 200 days before the controlled
// now under the harness agent's real memory room and a chat transcript dated
// 40 days before it in a real unified session store, and wires the agent
// MemoryStore's clock to the controlled now (SweepRetros compares the retro
// day-directory NAME against now − retentionDays). The in-memory config is
// loaded from the real config.json carrying retentionDoc.
func newRetentionSweepFixture(t *testing.T, retentionDoc string) *retentionSweepFixture {
	t.Helper()
	tmpDir := t.TempDir()
	agentWorkspace := filepath.Join(tmpDir, "agents", sweepTestAgentID)
	require.NoError(t, os.MkdirAll(agentWorkspace, 0o700))

	configPath := filepath.Join(tmpDir, "config.json")
	f := &retentionSweepFixture{configPath: configPath}
	f.writeRetentionDoc(t, retentionDoc)
	cfg := f.cfg
	cfg.Agents.Defaults.Home = tmpDir
	// The registry is seeded from the in-memory list (the file carries no
	// agents.list, matching the shared harness fixtures).
	cfg.Agents.List = []config.AgentConfig{{
		ID:   sweepTestAgentID,
		Name: "Sweep Test Agent",
		Type: config.AgentTypeCustom,
		Home: agentWorkspace,
	}}

	f.al = mustAgentLoop(t, cfg, bus.NewMessageBus(), &restMockProvider{})

	// Controlled clock on the real MemoryStore the sweep reaches through the
	// registry (executeRetroSweep walks registry → ContextBuilder.Memory()).
	inst, ok := f.al.GetRegistry().GetAgent(sweepTestAgentID)
	require.True(t, ok, "the harness agent must be registered")
	require.NotNil(t, inst, "the harness agent instance must exist")
	require.NotNil(t, inst.ContextBuilder, "the harness agent must have a context builder")
	mem := inst.ContextBuilder.Memory()
	require.NotNil(t, mem, "the harness agent must have a memory store")
	mem.SetClock(func() time.Time { return fixedSweepNow })

	// The reflection: retros/<YYYY-MM-DD>/<id>_retro.md under the agent's
	// private room (pkg/agent/memory.go::SweepRetros's layout), day dir
	// dated 200 days before the controlled now.
	retroDay := fixedSweepNow.AddDate(0, 0, -200).Format("2006-01-02")
	retroDir := filepath.Join(agentWorkspace, ".omnipus", "retros", retroDay)
	require.NoError(t, os.MkdirAll(retroDir, 0o700))
	f.retroPath = filepath.Join(retroDir, "sweep-fixtures_retro.md")
	require.NoError(t, os.WriteFile(f.retroPath, []byte("# Retrospective\n\n200 days old.\n"), 0o600))

	// The chat transcript: a real unified session store with one partition
	// file dated 40 days before the controlled now (the session sweep
	// deletes aged session directories — the pkg/session fixture pattern).
	store, err := session.NewUnifiedStore(filepath.Join(tmpDir, "sessions"))
	require.NoError(t, err, "NewUnifiedStore must succeed")
	t.Cleanup(func() { _ = store.Close() })
	f.store = store
	sessionDir := filepath.Join(store.BaseDir(), "sweep-fixture-session")
	require.NoError(t, os.MkdirAll(sessionDir, 0o700))
	f.transcript = filepath.Join(sessionDir, retroDay+".jsonl")
	require.NoError(t, os.WriteFile(f.transcript, []byte("{\"id\":\"sweep\"}\n"), 0o600))
	old := fixedSweepNow.AddDate(0, 0, -40)
	require.NoError(t, os.Chtimes(f.transcript, old, old), "the transcript must be dated 40 days ago")

	return f
}

// writeRetentionDoc rewrites config.json with the given storage.retention
// fragment and reloads the in-memory config from it — the "person saved a new
// value" step, done the way a restart would see it.
func (f *retentionSweepFixture) writeRetentionDoc(t *testing.T, retentionDoc string) {
	t.Helper()
	doc := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"storage":{` + retentionDoc + `}}`
	require.NoError(t, os.WriteFile(f.configPath, []byte(doc), 0o600))
	loaded, err := config.LoadConfig(f.configPath)
	require.NoError(t, err, "the seeded config.json must load")
	f.cfg = loaded
}

// getCfg returns the fixture's config — the thunk the production loops pass.
func (f *retentionSweepFixture) getCfg() *config.Config { return f.cfg }

// requireRetroGone / requireRetroKept / requireTranscriptGone /
// requireTranscriptKept are the four file-state oracles.
func requireRetroGone(t *testing.T, f *retentionSweepFixture, where string) {
	t.Helper()
	_, err := os.Stat(f.retroPath)
	require.True(t, errors.Is(err, os.ErrNotExist), "%s: the reflection must be deleted", where)
}

func requireRetroKept(t *testing.T, f *retentionSweepFixture, where string) {
	t.Helper()
	_, err := os.Stat(f.retroPath)
	require.NoError(t, err, "%s: the reflection must still exist", where)
}

func requireTranscriptGone(t *testing.T, f *retentionSweepFixture, where string) {
	t.Helper()
	_, err := os.Stat(f.transcript)
	require.True(t, errors.Is(err, os.ErrNotExist), "%s: the chat transcript must be deleted", where)
}

func requireTranscriptKept(t *testing.T, f *retentionSweepFixture, where string) {
	t.Helper()
	_, err := os.Stat(f.transcript)
	require.NoError(t, err, "%s: the chat transcript must still exist", where)
}

// runBothTicks executes the real retro tick and the real session sweep tick
// once each, synchronously — returning means both completed.
func runBothTicks(f *retentionSweepFixture) {
	executeRetroSweepTick(f.al, f.getCfg)
	executeSweepTick(f.store, f.getCfg)
}

// TestRetroSweep_MemoryForeverKeepsRetros — FR-049 / US-5 AS3. RED today:
// memory_retros_forever does not exist, so the sweep reads
// memory_retros_days=180 and deletes the 200-day-old reflection.
func TestRetroSweep_MemoryForeverKeepsRetros(t *testing.T) {
	f := newRetentionSweepFixture(t, retentionDocForever)

	runBothTicks(f)

	requireRetroKept(t, f, "with Keep forever on")
	requireTranscriptGone(t, f, "forever must stop ONLY the memory sweep — the 40-day-old transcript is past session_days 30 and is deleted")
}

// TestRetroSweep_ForeverOffDeletes — the positive control: the same fixture
// with forever off deletes the reflection, proving the instrument can see a
// deletion. Passes today and must stay green after GREEN (existing sweep
// behaviour preserved).
func TestRetroSweep_ForeverOffDeletes(t *testing.T) {
	f := newRetentionSweepFixture(t, retentionDocPlain)

	runBothTicks(f)

	requireRetroGone(t, f, "positive control: with forever off the 200-day-old reflection is past 180 days")
	requireTranscriptGone(t, f, "the 40-day-old transcript is past session_days 30")
}

// TestRetroSweep_ForeverOffResumes — "Turning Keep forever off again resumes
// deletion": the reflection is planted while forever is on (a first tick
// keeps it), the switch is saved off, and the NEXT tick deletes it. RED
// today: the first tick already deletes it (no forever switch exists).
func TestRetroSweep_ForeverOffResumes(t *testing.T) {
	f := newRetentionSweepFixture(t, retentionDocForever)

	// While forever is on, the tick keeps the reflection.
	executeRetroSweepTick(f.al, f.getCfg)
	requireRetroKept(t, f, "the planted reflection survives the sweep while Keep forever is on")

	// The person turns Keep forever off and saves; the next nightly sweep
	// applies memory_retros_days again.
	f.writeRetentionDoc(t, retentionDocPlain)
	executeRetroSweepTick(f.al, f.getCfg)

	requireRetroGone(t, f, "after Keep forever is saved off, the next sweep deletes the reflection that is older than memory_retros_days")
}

// TestRetroSweep_LegacyDisabledKeepsAll — FR-048: the legacy config-file-only
// `disabled: true` keeps stopping BOTH sweeps. Existing behaviour (the
// IsDisabled checks at both ticks); must stay green after GREEN.
func TestRetroSweep_LegacyDisabledKeepsAll(t *testing.T) {
	f := newRetentionSweepFixture(t, retentionDocLegacyDisabled)

	runBothTicks(f)

	requireRetroKept(t, f, "the legacy keep-everything switch stops the memory sweep")
	requireTranscriptKept(t, f, "the legacy keep-everything switch stops the chat sweep — the 40-day-old transcript survives even though session_days is 30")
}
