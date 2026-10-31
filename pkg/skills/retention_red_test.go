package skills

// RED tests — landing/skills (T9): retention counts supersession, not
// creation (§9.1, r3 R2-CRIT-001). Scenarios S-5.6, S-5.7, S-5.8, S-5.13,
// S-5.14, S-11.18's sweep halves; dataset D-4.
// Traces to: skills-spec-r13.md §9.1, §16 US-5, §17.1 rows 16/38, §17.2 D-4.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyEntry mirrors §5.11's manifest shape (only the fields the retention
// rule reads plus enough to be a valid manifest).
type historyEntry struct {
	ID         string   `json:"id"`
	Parent     string   `json:"parent,omitempty"`
	CreatedAt  string   `json:"created_at"`
	Slug       string   `json:"slug"`
	Kind       string   `json:"kind"`
	Actor      string   `json:"actor"`
	Files      []any    `json:"files"`
	Paths      []string `json:"paths"`
	PathsTotal int      `json:"paths_total"`
	Revision   string   `json:"revision"`
}

// seedHistory writes a manifest chain under historyRoot/<slug>/versions/ plus
// one blob per version so rule 3 (unreferenced-blob removal) is observable.
func seedHistory(t *testing.T, historyRoot, slug string, entries []historyEntry) string {
	t.Helper()
	versionsDir := filepath.Join(historyRoot, slug, "versions")
	require.NoError(t, os.MkdirAll(versionsDir, 0o755))
	blobsDir := filepath.Join(historyRoot, slug, "blobs")
	require.NoError(t, os.MkdirAll(blobsDir, 0o755))
	for i, e := range entries {
		raw, err := json.Marshal(e)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(versionsDir, e.ID+".json"), raw, 0o644))
		require.NoError(t, os.WriteFile(filepath.Join(blobsDir, e.Revision), []byte{byte('a' + i)}, 0o644))
	}
	return blobsDir
}

// daysAgo renders the UTC timestamp format of §5.11 (created_at).
func daysAgo(now time.Time, days int) string {
	return now.AddDate(0, 0, -days).UTC().Format("20060102T150405Z")
}

// manifestIDs lists the retained manifest ids (minus .json) newest-first.
func manifestIDs(t *testing.T, historyRoot, slug string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(historyRoot, slug, "versions"))
	require.NoError(t, err)
	ids := make([]string, 0, len(entries))
	for _, e := range entries {
		ids = append(ids, e.Name()[:len(e.Name())-len(".json")])
	}
	return ids
}

// TestRetention_SupersessionClock (S-5.6). BDD: Given retention 90 and chain
// V1 created 120d ago, V2 created 100d ago (supersedes V1), V3 created 10d
// ago, V4 today (newest) — When the sweep runs, Then V1 is removed (its
// successor V2 is 100 days old), V2/V3/V4 kept, unreferenced blobs removed.
// A version's own creation age decides nothing — V2 is 100 days old and
// retained.
// Traces to: skills-spec-r13.md §9.1 rule 1, S-5.6, D-4.
func TestRetention_SupersessionClock(t *testing.T) {
	now := time.Now().UTC()
	historyRoot := t.TempDir()
	blobs := seedHistory(t, historyRoot, "app", []historyEntry{
		{ID: "v1", CreatedAt: daysAgo(now, 120), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b1"},
		{ID: "v2", Parent: "v1", CreatedAt: daysAgo(now, 100), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b2"},
		{ID: "v3", Parent: "v2", CreatedAt: daysAgo(now, 10), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b3"},
		{ID: "v4", Parent: "v3", CreatedAt: daysAgo(now, 0), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b4"},
	})

	require.NoError(t, RetentionSweep(historyRoot, 90, false, now))

	ids := manifestIDs(t, historyRoot, "app")
	assert.NotContains(t, ids, "v1", "V1 removed: its successor V2 superseded it 100 days ago")
	for _, keep := range []string{"v2", "v3", "v4"} {
		assert.Contains(t, ids, keep, "V2's own 100-day age is irrelevant; V3, V4 (newest) retained")
	}

	// Unreferenced blobs removed (rule 3): b1 is referenced only by v1.
	_, err := os.Stat(filepath.Join(blobs, "b1"))
	assert.True(t, os.IsNotExist(err), "the blob only v1 referenced is removed")
	for _, keep := range []string{"b2", "b3", "b4"} {
		assert.FileExists(t, filepath.Join(blobs, keep), "referenced blobs kept")
	}
}

// TestRetention_DeleteHeadKeepsPreDeletionState_FullWindow (S-5.13).
// BDD: Given the newest content version V1 created 100 days ago (retention
// 90) and the owner deleting today (V2 = delete_skill, age 0), an immediate
// sweep retains V1 with its full file list despite its age; boundary both
// ways on the successor age; the whole history goes exactly when the
// delete_skill manifest passes 90 days.
// Traces to: skills-spec-r13.md §9.1 rule 2, S-5.13.
func TestRetention_DeleteHeadKeepsPreDeletionState_FullWindow(t *testing.T) {
	now := time.Now().UTC()

	t.Run("immediate sweep after delete keeps the 100-day-old pre-deletion state", func(t *testing.T) {
		historyRoot := t.TempDir()
		seedHistory(t, historyRoot, "app", []historyEntry{
			{ID: "v1", CreatedAt: daysAgo(now, 100), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b1"},
			{ID: "v2", Parent: "v1", CreatedAt: daysAgo(now, 0), Slug: "app", Kind: "delete_skill", Actor: "agent", Revision: ""},
		})
		require.NoError(t, RetentionSweep(historyRoot, 90, false, now))
		ids := manifestIDs(t, historyRoot, "app")
		assert.Contains(t, ids, "v1", "rule 2: while the delete_skill head is newest, every older manifest is retained regardless of age")
		assert.Contains(t, ids, "v2", "the newest manifest is never deleted")
	})

	t.Run("successor age 91 days removes / 89 days keeps", func(t *testing.T) {
		// 91-day successor: V1 prunable (rule 1 governs — no delete head).
		h1 := t.TempDir()
		seedHistory(t, h1, "app", []historyEntry{
			{ID: "v1", CreatedAt: daysAgo(now, 200), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b1"},
			{ID: "v2", Parent: "v1", CreatedAt: daysAgo(now, 91), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b2"},
		})
		require.NoError(t, RetentionSweep(h1, 90, false, now))
		assert.NotContains(t, manifestIDs(t, h1, "app"), "v1", "successor 91 days old → removed")

		// 89-day successor: kept.
		h2 := t.TempDir()
		seedHistory(t, h2, "app", []historyEntry{
			{ID: "v1", CreatedAt: daysAgo(now, 200), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b1"},
			{ID: "v2", Parent: "v1", CreatedAt: daysAgo(now, 89), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b2"},
		})
		require.NoError(t, RetentionSweep(h2, 90, false, now))
		assert.Contains(t, manifestIDs(t, h2, "app"), "v1", "successor 89 days old → kept")
	})

	t.Run("history directory removed exactly when the delete_skill manifest passes 90 days", func(t *testing.T) {
		h := t.TempDir()
		seedHistory(t, h, "app", []historyEntry{
			{ID: "v1", CreatedAt: daysAgo(now, 100), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b1"},
			{ID: "v2", Parent: "v1", CreatedAt: daysAgo(now, 91), Slug: "app", Kind: "delete_skill", Actor: "agent", Revision: ""},
		})
		require.NoError(t, RetentionSweep(h, 90, false, now))
		_, err := os.Stat(filepath.Join(h, "app"))
		assert.True(t, os.IsNotExist(err), "the whole history directory is deleted once the delete_skill manifest itself is older than the window")
	})
}

// TestRetention_NewestSupersededToday_Kept (S-5.14): no delete — a version
// superseded today stays regardless of its own age.
// Traces to: skills-spec-r13.md §9.1 rule 1, S-5.14.
func TestRetention_NewestSupersededToday_Kept(t *testing.T) {
	now := time.Now().UTC()
	historyRoot := t.TempDir()
	seedHistory(t, historyRoot, "app", []historyEntry{
		{ID: "v1", CreatedAt: daysAgo(now, 100), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b1"},
		{ID: "v2", Parent: "v1", CreatedAt: daysAgo(now, 0), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b2"},
	})
	require.NoError(t, RetentionSweep(historyRoot, 90, false, now))
	assert.Contains(t, manifestIDs(t, historyRoot, "app"), "v1",
		"V1 was superseded today — the full window starts now")
}

// TestRetention_Disabled_KeepsCommittedHistory_NeverTouchesCaptureTemporaries
// (S-5.8 + §9.1 scope rule). BDD: Given retention disabled, a sweep removes
// nothing from committed history — and per §9.1's scope (r9) a capture
// temporary under blobs/.incoming-* is NOT its concern either.
// Traces to: skills-spec-r13.md §9.1 scope + IsDisabled, S-5.8.
func TestRetention_Disabled_KeepsCommittedHistory_NeverTouchesCaptureTemporaries(t *testing.T) {
	now := time.Now().UTC()
	historyRoot := t.TempDir()
	blobs := seedHistory(t, historyRoot, "app", []historyEntry{
		{ID: "v1", CreatedAt: daysAgo(now, 365), Slug: "app", Kind: "edit", Actor: "agent", Revision: "b1"},
	})
	incoming := filepath.Join(blobs, ".incoming-attempt1")
	require.NoError(t, os.MkdirAll(incoming, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(incoming, "partial"), []byte("x"), 0o644))

	require.NoError(t, RetentionSweep(historyRoot, 90, true, now))

	assert.Contains(t, manifestIDs(t, historyRoot, "app"), "v1",
		"disabled keeps every committed version, however old")
	assert.DirExists(t, incoming,
		"§9.1 scope: the sweep never touches capture temporaries (§9.5/§9.3 own them)")
}

// TestRetention_S11_18HeadRules (S-11.18's sweep halves). BDD: Given chain
// V1 (content, superseded >90d ago by V2, itself created 100d ago) → V3
// delete_skill head (today): the FIRST sweep applies rule 2 (V1 kept although
// its successor is >90 days — the delete_skill head is still newest); after a
// content version V4 commits (the detection trigger's effect), the SECOND
// sweep applies rule 1 (V1 removed, V2/V3 kept by young successors, V4 newest
// never removed).
// Traces to: skills-spec-r13.md §9.1 rules 1–2, §7.1 step 6, S-11.18.
func TestRetention_S11_18HeadRules(t *testing.T) {
	now := time.Now().UTC()
	historyRoot := t.TempDir()
	blobs := seedHistory(t, historyRoot, "foo", []historyEntry{
		{ID: "v1", CreatedAt: daysAgo(now, 200), Slug: "foo", Kind: "edit", Actor: "agent", Revision: "b1"},
		{ID: "v2", Parent: "v1", CreatedAt: daysAgo(now, 100), Slug: "foo", Kind: "edit", Actor: "agent", Revision: "b2"},
		{ID: "v3", Parent: "v2", CreatedAt: daysAgo(now, 0), Slug: "foo", Kind: "delete_skill", Actor: "agent", Revision: ""},
	})

	// First sweep: the delete_skill head V3 is still newest → rule 2.
	require.NoError(t, RetentionSweep(historyRoot, 90, false, now))
	ids := manifestIDs(t, historyRoot, "foo")
	assert.Contains(t, ids, "v1", "first sweep asserts RULE 2: V1 kept although successor V2 superseded it >90 days ago")
	assert.Contains(t, ids, "v2")
	assert.Contains(t, ids, "v3")

	// The detection trigger commits V4 (external_change of the recreated
	// folder) — newest no longer delete_skill; rule 1 governs again.
	seedHistory(t, historyRoot, "foo", []historyEntry{
		{ID: "v4", Parent: "v3", CreatedAt: daysAgo(now, 0), Slug: "foo", Kind: "external_change", Actor: "unknown", Revision: "b4"},
	})
	require.NoError(t, RetentionSweep(historyRoot, 90, false, now))
	ids = manifestIDs(t, historyRoot, "foo")
	assert.NotContains(t, ids, "v1", "second sweep asserts RULE 1: V1 removed (successor >90d)")
	assert.Contains(t, ids, "v2", "V2 kept (successor V3 is today)")
	assert.Contains(t, ids, "v3", "V3 kept (successor V4 is today)")
	assert.Contains(t, ids, "v4", "V4 newest — never removed")
	_, err := os.Stat(filepath.Join(blobs, "b1"))
	assert.True(t, os.IsNotExist(err), "the blob only V1 referenced is removed by rule 3")
}
