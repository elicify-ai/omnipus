// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

// memory_retros_recall_window_test.go — Settings redesign #1519, RED.
//
// Test Implementation Order row 8: TestSearchRetros_FollowsRetentionWindow
// [pending FQ-1] — FR-050, written for the RECOMMENDED answer A ("recall
// reaches as far back as memory is kept"). If the founder answers FQ-1 B,
// FR-050 and US-5 AS9 are dropped and this file is dropped with them (spec
// "Left open" table). The pending marker is deliberate: this is the one
// spec row whose fate is an open founder question.
//
// Oracle: the spec BDD scenario "[Pending FQ-1] An old reflection kept by
// the setting can be recalled" — memory_retros_days 730 and a reflection
// 400 days old containing the word are found — and FR-050: recall reads as
// far back as memory retention keeps the reflections (memory_retros_days up
// to 730; no age limit while Keep forever or the legacy switch is on, with
// a backend-lead-stated bounded read). Every expected value comes from the
// spec, never from SearchRetros's current output.
//
// RED today: SearchRetros hard-codes ReadRetros(365) (memory_adapter.go)
// and ReadRetros clamps daysBack to 365 (memory.go), so anything older
// than 365 days is invisible no matter what is kept — the 400-day-old
// "zeppelin" reflection of the BDD scenario is not found, and the forever
// and legacy cases cannot lift the limit at all.
//
// Unit boundary (test plan: "real SearchRetros with 400-day, 730-day and
// older content; no stub"): a real AgentLoop built from a config carrying
// the retention block, the production MemoryStore it constructs (reached
// through the registry), and the real adapter + BM25 ranking — reflection
// files are real files in the real retros directory layout, dated with the
// store's own clock seam (MemoryStore.SetClock). No memory stub anywhere.
//
// GREEN wiring note (test-side reconciliation stays with this test's
// author): the retention precondition below is expressed the only way
// available before the GREEN seam exists — the raw storage.retention block
// in config.json, loaded with config.LoadConfig. Today the loader ignores
// the unknown keys; once GREEN adds them to the config struct, the same
// file loads into the struct and the production wiring carries them to the
// read. If GREEN lands the retention on a constructor parameter instead,
// only this file's cfg-building helper changes; every assertion is frozen.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/config"
)

// recallWindowNow is the controlled clock for every reflection date below.
var recallWindowNow = time.Date(2026, 11, 20, 3, 0, 0, 0, time.UTC)

// recallWindowAgentID is the one registered agent of this fixture.
const recallWindowAgentID = "01JXRECALLWINDOWTEST001"

// writeRecallWindowConfig writes config.json carrying the raw
// storage.retention block (see the GREEN wiring note) and returns the
// loaded config, with the harness agent attached.
func writeRecallWindowConfig(t *testing.T, home string, retentionDoc string) *config.Config {
	t.Helper()
	doc := `{"version":1,"agents":{"defaults":{},"list":[]},"providers":[],"storage":{` + retentionDoc + `}}`
	path := filepath.Join(home, "config.json")
	require.NoError(t, os.WriteFile(path, []byte(doc), 0o600))
	cfg, err := config.LoadConfig(path)
	require.NoError(t, err, "the seeded config.json must load")
	cfg.Agents.Defaults.Home = home
	cfg.Agents.List = []config.AgentConfig{{
		ID:   recallWindowAgentID,
		Name: "Recall Window Agent",
		Type: config.AgentTypeCustom,
		Home: filepath.Join(home, "agents", recallWindowAgentID),
	}}
	return cfg
}

// plantRetro writes one parseable retro file dated daysBefore the controlled
// now, containing the given distinctive word in its recap.
func plantRetro(t *testing.T, memoryRoot string, daysBefore int, word string) string {
	t.Helper()
	day := recallWindowNow.AddDate(0, 0, -daysBefore)
	dayDir := filepath.Join(memoryRoot, ".omnipus", "retros", day.Format("2006-01-02"))
	require.NoError(t, os.MkdirAll(dayDir, 0o700))
	body := "<!-- ts=" + day.Format("2006-01-02T15:04:05.000Z") + " trigger=session_end -->\n" +
		"## Session recap\n" +
		"The " + word + " log entry was recorded for the retention window study.\n" +
		"<!-- next -->\n"
	path := filepath.Join(dayDir, word+"_retro.md")
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	return path
}

// recallWindowFixture is one real loop + production memory store with the
// three planted reflections.
type recallWindowFixture struct {
	adapter *MemoryStoreAdapter
}

// newRecallWindowFixture builds the real loop from a retention doc, plants
// the three reflections (400 days, 729 days, 800 days old) under the
// production MemoryStore's retros directory, pins the store's clock to the
// controlled now, and wraps the SAME store the production wiring uses in
// the real adapter.
func newRecallWindowFixture(t *testing.T, retentionDoc string) *recallWindowFixture {
	t.Helper()
	home := t.TempDir()
	cfg := writeRecallWindowConfig(t, home, retentionDoc)

	al := mustNewAgentLoop(t, cfg, bus.NewMessageBus(), &mockProvider{})
	t.Cleanup(al.Close)

	inst, ok := al.GetRegistry().GetAgent(recallWindowAgentID)
	require.True(t, ok, "the fixture agent must be registered")
	require.NotNil(t, inst, "the fixture agent instance must exist")
	require.NotNil(t, inst.ContextBuilder, "the fixture agent must have a context builder")
	mem := inst.ContextBuilder.Memory()
	require.NotNil(t, mem, "the fixture agent must have a memory store")
	mem.SetClock(func() time.Time { return recallWindowNow })
	retroRoot := inst.Home

	plantRetro(t, retroRoot, 400, "zeppelin") // the BDD scenario's reflection
	plantRetro(t, retroRoot, 729, "voyager")  // just inside a 730-day keep
	plantRetro(t, retroRoot, 800, "cauldron") // older than the longest day keep

	return &recallWindowFixture{adapter: NewMemoryStoreAdapter(mem)}
}

// searchFinds drives the real SearchRetros and reports whether any returned
// retrospective's content carries the word.
func (f *recallWindowFixture) searchFinds(t *testing.T, word string) bool {
	t.Helper()
	entries, err := f.adapter.SearchRetros(word, 20)
	require.NoError(t, err, "SearchRetros must not fail")
	for _, e := range entries {
		if strings.Contains(e.Content, word) {
			return true
		}
	}
	return false
}

func TestSearchRetros_FollowsRetentionWindow(t *testing.T) {
	t.Run("kept 730 days: the 400-day-old reflection is recallable (BDD: zeppelin)", func(t *testing.T) {
		f := newRecallWindowFixture(t, `"retention":{"session_days":30,"memory_retros_days":730}`)
		found := f.searchFinds(t, "zeppelin")
		require.True(t, found,
			"RED (FR-050, FQ-1 answer A): a reflection 400 days old that memory retention keeps "+
				"(memory_retros_days 730) must be recallable — today recall reads at most 365 days, "+
				"so the zeppelin reflection of the BDD scenario cannot be found")
	})

	t.Run("kept 730 days: the 729-day-old reflection is recallable", func(t *testing.T) {
		f := newRecallWindowFixture(t, `"retention":{"session_days":30,"memory_retros_days":730}`)
		found := f.searchFinds(t, "voyager")
		require.True(t, found,
			"RED (FR-050, FQ-1 answer A): a reflection just inside the 730-day keep must be "+
				"recallable — the upper boundary of the kept window is recallable, not clamped to 365")
	})

	t.Run("kept 730 days: content older than the keep is not recalled", func(t *testing.T) {
		f := newRecallWindowFixture(t, `"retention":{"session_days":30,"memory_retros_days":730}`)
		found := f.searchFinds(t, "cauldron")
		require.False(t, found,
			"FR-050: recall reaches as far back as memory retention KEEPS them — "+
				"an 800-day-old reflection is past a 730-day keep and is outside the read window")
	})

	t.Run("Keep forever on: no age limit", func(t *testing.T) {
		f := newRecallWindowFixture(t, `"retention":{"session_days":30,"memory_retros_days":180,"memory_retros_forever":true}`)
		found := f.searchFinds(t, "cauldron")
		require.True(t, found,
			"RED (FR-050, FQ-1 answer A): with Keep forever on the memory is kept with no age "+
				"limit, so recall must reach the 800-day-old reflection — today the 365-day clamp hides it")
	})

	t.Run("legacy disabled on: no age limit", func(t *testing.T) {
		f := newRecallWindowFixture(t, `"retention":{"session_days":30,"memory_retros_days":180,"disabled":true}`)
		found := f.searchFinds(t, "cauldron")
		require.True(t, found,
			"RED (FR-050, FQ-1 answer A): with the legacy keep-everything switch on, memory is kept "+
				"forever and recall must reach the 800-day-old reflection — today the 365-day clamp hides it")
	})
}
