package skills

// RED tests — landing/skills (T9): retention counts supersession, not
// creation (§9.1, r3 R2-CRIT-001). Scenarios S-5.6, S-5.7, S-5.8, S-5.13,
// S-5.14, S-11.18's sweep halves; dataset D-4.
// Traces to: skills-spec-r15.md §9.1, §16 US-5, §17.1 rows 16/38, §17.2 D-4.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// historyEntry mirrors §5.11's manifest shape (only the fields the retention
// rule reads plus enough to be a valid manifest).
type historyEntry struct {
	ID         string        `json:"id"`
	Parent     string        `json:"parent,omitempty"`
	CreatedAt  string        `json:"created_at"`
	Slug       string        `json:"slug"`
	Kind       string        `json:"kind"`
	Actor      string        `json:"actor"`
	Files      []historyFile `json:"files"`
	Paths      []string      `json:"paths"`
	PathsTotal int           `json:"paths_total"`
	Revision   string        `json:"revision"`
}

// historyFile mirrors §5.11's manifest files[] entry: the blob store is
// content-addressed by sha256, so "referenced" (rule 3) means named by a
// retained manifest's files[].sha256.
type historyFile struct {
	Path   string `json:"path"`
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
	Exec   bool   `json:"exec,omitempty"`
}

// blobBytes regenerates a fixture blob's bytes deterministically from the
// file entry — the same bytes mkFile hashed, so seedHistory can verify the
// fixture is self-consistent (an address that does not address its content
// would make rule 3's oracle meaningless).
func blobBytes(f historyFile) []byte {
	pattern := []byte(f.Path + "|omnipus-retention-fixture|")
	if f.Path == "SKILL.md" {
		header := "---\nname: app\ndescription: retention fixture\n---\n"
		if f.Size < int64(len(header)) {
			panic("invalid retention fixture size")
		}
		return []byte(header + strings.Repeat("x", int(f.Size)-len(header)))
	}
	out := make([]byte, f.Size)
	for i := range out {
		out[i] = pattern[i%len(pattern)]
	}
	return out
}

// mkFile builds a files[] entry whose sha256 addresses real deterministic
// bytes of the requested size.
func mkFile(path string, size int64) historyFile {
	f := historyFile{Path: path, Size: size}
	sum := sha256.Sum256(blobBytes(f))
	f.SHA256 = hex.EncodeToString(sum[:])
	return f
}

// seedHistory writes a manifest chain under historyRoot/<slug>/versions/
// plus one content-addressed blob per files[] entry the manifests reference,
// so rule 3 (unreferenced-blob removal) is observable: a delete_skill
// manifest references no files and writes no blob. Verifies every written
// blob's bytes hash to its recorded address.
func seedHistory(t *testing.T, historyRoot, slug string, entries []historyEntry) string {
	t.Helper()
	versionsDir := filepath.Join(historyRoot, slug, "versions")
	require.NoError(t, os.MkdirAll(versionsDir, 0o755))
	blobsDir := filepath.Join(historyRoot, slug, "blobs")
	require.NoError(t, os.MkdirAll(blobsDir, 0o755))
	for _, e := range entries {
		e.Slug = slug
		if e.Paths == nil {
			for _, f := range e.Files {
				e.Paths = append(e.Paths, f.Path)
			}
			e.PathsTotal = len(e.Paths)
		}
		if e.Files == nil {
			e.Files = []historyFile{}
		}
		if e.Paths == nil {
			e.Paths = []string{}
		}
		snapshot := t.TempDir()
		content := map[string][]byte{}
		for i, f := range e.Files {
			raw := blobBytes(f)
			if f.Path == "SKILL.md" && slug != "app" {
				raw = []byte(strings.Replace(string(raw), "name: app\n", "name: "+slug+"\n", 1))
				sum := sha256.Sum256(raw)
				f.SHA256 = hex.EncodeToString(sum[:])
				f.Size = int64(len(raw))
				e.Files[i] = f
			}
			content[f.Path] = raw
			p := filepath.Join(snapshot, filepath.FromSlash(f.Path))
			require.NoError(t, os.MkdirAll(filepath.Dir(p), 0o755))
			mode := os.FileMode(0o644)
			if f.Exec {
				mode = 0o755
			}
			require.NoError(t, os.WriteFile(p, raw, mode))
		}
		if e.Kind != "delete_skill" {
			var revisionErr error
			e.Revision, revisionErr = ContentRevision(snapshot)
			require.NoError(t, revisionErr, "BLOCKED: content revision for self-consistent history fixtures not implemented — r15 §5.11/§7.3")
		}
		raw, err := json.Marshal(e)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(versionsDir, e.ID+".json"), raw, 0o644))
		for _, f := range e.Files {
			bytes := content[f.Path]
			sum := sha256.Sum256(bytes)
			require.Equal(t, f.SHA256, hex.EncodeToString(sum[:]),
				"fixture self-check: blob %s addresses its bytes", f.SHA256[:12])
			require.NoError(t, os.WriteFile(filepath.Join(blobsDir, f.SHA256), bytes, 0o644))
		}
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
		if strings.HasSuffix(e.Name(), ".json") {
			ids = append(ids, strings.TrimSuffix(e.Name(), ".json"))
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(ids)))
	return ids
}

// TestRetention_SupersessionClock (S-5.6). BDD: Given retention 90 and chain
// V1 created 120d ago, V2 created 100d ago (supersedes V1), V3 created 10d
// ago, V4 today (newest) — When the sweep runs, Then V1 is removed (its
// successor V2 is 100 days old), V2/V3/V4 kept, unreferenced blobs removed.
// A version's own creation age decides nothing — V2 is 100 days old and
// retained.
// Traces to: skills-spec-r15.md §9.1 rule 1, S-5.6, D-4.
func TestRetention_SupersessionClock(t *testing.T) {
	now := time.Now().UTC()
	historyRoot := t.TempDir()
	f1 := mkFile("SKILL.md", 100)
	f2 := mkFile("SKILL.md", 120)
	f3 := mkFile("SKILL.md", 140)
	f4 := mkFile("SKILL.md", 160)
	blobs := seedHistory(t, historyRoot, "app", []historyEntry{
		{ID: fixtureVersionID(1), CreatedAt: daysAgo(now, 120), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{f1}},
		{ID: fixtureVersionID(2), Parent: fixtureVersionID(1), CreatedAt: daysAgo(now, 100), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{f2}},
		{ID: fixtureVersionID(3), Parent: fixtureVersionID(2), CreatedAt: daysAgo(now, 10), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{f3}},
		{ID: fixtureVersionID(4), Parent: fixtureVersionID(3), CreatedAt: daysAgo(now, 0), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{f4}},
	})

	require.NoError(t, RetentionSweep(historyRoot, 90, false, now))

	ids := manifestIDs(t, historyRoot, "app")
	assert.NotContains(t, ids, fixtureVersionID(1), "V1 removed: its successor V2 superseded it 100 days ago")
	for _, keep := range []string{fixtureVersionID(2), fixtureVersionID(3), fixtureVersionID(4)} {
		assert.Contains(t, ids, keep, "V2's own 100-day age is irrelevant; V3, V4 (newest) retained")
	}

	// Unreferenced blobs removed (rule 3): f1's blob is referenced only by
	// v1's files[] — after v1 is pruned nothing references it.
	_, err := os.Stat(filepath.Join(blobs, f1.SHA256))
	assert.True(t, os.IsNotExist(err), "the blob only v1 referenced is removed")
	for _, keep := range []historyFile{f2, f3, f4} {
		assert.FileExists(t, filepath.Join(blobs, keep.SHA256), "referenced blob kept")
	}
}

// TestRetention_DeleteHeadKeepsPreDeletionState_FullWindow (S-5.13).
// BDD: Given the newest content version V1 created 100 days ago (retention
// 90) and the owner deleting today (V2 = delete_skill, age 0), an immediate
// sweep retains V1 with its full file list despite its age; boundary both
// ways on the successor age; the whole history goes exactly when the
// delete_skill manifest passes 90 days.
// Traces to: skills-spec-r15.md §9.1 rule 2, S-5.13.
func TestRetention_DeleteHeadKeepsPreDeletionState_FullWindow(t *testing.T) {
	now := time.Now().UTC()

	t.Run("immediate sweep after delete keeps the 100-day-old pre-deletion state", func(t *testing.T) {
		historyRoot := t.TempDir()
		f1 := mkFile("SKILL.md", 80)
		blobs := seedHistory(t, historyRoot, "app", []historyEntry{
			{ID: fixtureVersionID(1), CreatedAt: daysAgo(now, 100), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{f1}},
			{ID: fixtureVersionID(2), Parent: fixtureVersionID(1), CreatedAt: daysAgo(now, 0), Slug: "app", Kind: "delete_skill", Actor: "agent"},
		})
		seedPrunableControl(t, historyRoot, now)
		require.NoError(t, RetentionSweep(historyRoot, 90, false, now))
		assertPrunedControl(t, historyRoot)
		ids := manifestIDs(t, historyRoot, "app")
		assert.Contains(t, ids, fixtureVersionID(1), "rule 2: while the delete_skill head is newest, every older manifest is retained regardless of age")
		assert.Contains(t, ids, fixtureVersionID(2), "the newest manifest is never deleted")
		assert.FileExists(t, filepath.Join(blobs, f1.SHA256),
			"the retained pre-deletion state keeps its blob for the full window")
	})

	t.Run("successor age 91 days removes / 89 days keeps", func(t *testing.T) {
		// 91-day successor: V1 prunable (rule 1 governs — no delete head).
		h1 := t.TempDir()
		seedHistory(t, h1, "app", []historyEntry{
			{ID: fixtureVersionID(1), CreatedAt: daysAgo(now, 200), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{mkFile("SKILL.md", 60)}},
			{ID: fixtureVersionID(2), Parent: fixtureVersionID(1), CreatedAt: daysAgo(now, 91), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{mkFile("SKILL.md", 70)}},
		})
		require.NoError(t, RetentionSweep(h1, 90, false, now))
		assert.NotContains(t, manifestIDs(t, h1, "app"), fixtureVersionID(1), "successor 91 days old → removed")

		// 89-day successor: kept.
		h2 := t.TempDir()
		seedHistory(t, h2, "app", []historyEntry{
			{ID: fixtureVersionID(1), CreatedAt: daysAgo(now, 200), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{mkFile("SKILL.md", 60)}},
			{ID: fixtureVersionID(2), Parent: fixtureVersionID(1), CreatedAt: daysAgo(now, 89), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{mkFile("SKILL.md", 70)}},
		})
		require.NoError(t, RetentionSweep(h2, 90, false, now))
		assert.Contains(t, manifestIDs(t, h2, "app"), fixtureVersionID(1), "successor 89 days old → kept")
	})

	t.Run("history directory removed exactly when the delete_skill manifest passes 90 days", func(t *testing.T) {
		h := t.TempDir()
		seedHistory(t, h, "app", []historyEntry{
			{ID: fixtureVersionID(1), CreatedAt: daysAgo(now, 100), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{mkFile("SKILL.md", 80)}},
			{ID: fixtureVersionID(2), Parent: fixtureVersionID(1), CreatedAt: daysAgo(now, 91), Slug: "app", Kind: "delete_skill", Actor: "agent"},
		})
		require.NoError(t, RetentionSweep(h, 90, false, now))
		_, err := os.Stat(filepath.Join(h, "app"))
		assert.True(t, os.IsNotExist(err), "the whole history directory is deleted once the delete_skill manifest itself is older than the window")
	})
}

// TestRetention_NewestSupersededToday_Kept (S-5.14): no delete — a version
// superseded today stays regardless of its own age.
// Traces to: skills-spec-r15.md §9.1 rule 1, S-5.14.
func TestRetention_NewestSupersededToday_Kept(t *testing.T) {
	now := time.Now().UTC()
	historyRoot := t.TempDir()
	seedHistory(t, historyRoot, "app", []historyEntry{
		{ID: fixtureVersionID(1), CreatedAt: daysAgo(now, 100), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{mkFile("SKILL.md", 64)}},
		{ID: fixtureVersionID(2), Parent: fixtureVersionID(1), CreatedAt: daysAgo(now, 0), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{mkFile("SKILL.md", 72)}},
	})
	seedPrunableControl(t, historyRoot, now)
	require.NoError(t, RetentionSweep(historyRoot, 90, false, now))
	assertPrunedControl(t, historyRoot)
	assert.Contains(t, manifestIDs(t, historyRoot, "app"), fixtureVersionID(1),
		"V1 was superseded today — the full window starts now")
}

// TestRetention_Disabled_KeepsCommittedHistory_NeverTouchesCaptureTemporaries
// (S-5.8 + §9.1 scope rule). BDD: Given retention disabled, a sweep removes
// nothing from committed history — and per §9.1's scope (r9) a capture
// temporary under blobs/.incoming-* is NOT its concern either.
// Traces to: skills-spec-r15.md §9.1 scope + IsDisabled, S-5.8.
func TestRetention_Disabled_KeepsCommittedHistory_NeverTouchesCaptureTemporaries(t *testing.T) {
	now := time.Now().UTC()
	historyRoot := t.TempDir()
	blobs := seedHistory(t, historyRoot, "app", []historyEntry{
		{ID: fixtureVersionID(1), CreatedAt: daysAgo(now, 365), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{mkFile("SKILL.md", 64)}},
	})
	incoming := filepath.Join(blobs, ".incoming-attempt1")
	require.NoError(t, os.MkdirAll(incoming, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(incoming, "partial"), []byte("x"), 0o644))

	seedPrunableControl(t, historyRoot, now)
	require.NoError(t, RetentionSweep(historyRoot, 90, true, now))
	assert.Contains(t, manifestIDs(t, historyRoot, "prunable-control"), fixtureVersionID(1))

	assert.Contains(t, manifestIDs(t, historyRoot, "app"), fixtureVersionID(1),
		"disabled keeps every committed version, however old")
	assert.DirExists(t, incoming,
		"§9.1 scope: the sweep never touches capture temporaries (§9.5/§9.3 own them)")
	require.NoError(t, RetentionSweep(historyRoot, 90, false, now))
	assertPrunedControl(t, historyRoot)
	assert.DirExists(t, incoming, "even enabled retention does not own capture scratch")
}

// TestRetention_S11_18HeadRules (S-11.18's sweep halves). BDD: Given chain
// V1 (content, superseded >90d ago by V2, itself created 100d ago) → V3
// delete_skill head (today): the FIRST sweep applies rule 2 (V1 kept although
// its successor is >90 days — the delete_skill head is still newest); after a
// content version V4 commits (the detection trigger's effect), the SECOND
// sweep applies rule 1 (V1 removed, V2/V3 kept by young successors, V4 newest
// never removed).
// Traces to: skills-spec-r15.md §9.1 rules 1–2, §7.1 step 6, S-11.18.
func TestRetention_S11_18HeadRules(t *testing.T) {
	now := time.Now().UTC()
	historyRoot := t.TempDir()
	f1 := mkFile("SKILL.md", 88)
	f2 := mkFile("SKILL.md", 92)
	f4 := mkFile("SKILL.md", 96)
	blobs := seedHistory(t, historyRoot, "app", []historyEntry{
		{ID: fixtureVersionID(1), CreatedAt: daysAgo(now, 200), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{f1}},
		{ID: fixtureVersionID(2), Parent: fixtureVersionID(1), CreatedAt: daysAgo(now, 100), Slug: "app", Kind: "edit", Actor: "agent", Files: []historyFile{f2}},
		{ID: fixtureVersionID(3), Parent: fixtureVersionID(2), CreatedAt: daysAgo(now, 0), Slug: "app", Kind: "delete_skill", Actor: "agent"},
	})

	// First sweep: the delete_skill head V3 is still newest → rule 2.
	require.NoError(t, RetentionSweep(historyRoot, 90, false, now))
	ids := manifestIDs(t, historyRoot, "app")
	assert.Contains(t, ids, fixtureVersionID(1), "first sweep asserts RULE 2: V1 kept although successor V2 superseded it >90 days ago")
	assert.Contains(t, ids, fixtureVersionID(2))
	assert.Contains(t, ids, fixtureVersionID(3))

	// The detection trigger commits V4 (external_change of the recreated
	// folder) — newest no longer delete_skill; rule 1 governs again.
	seedHistory(t, historyRoot, "app", []historyEntry{
		{ID: fixtureVersionID(4), Parent: fixtureVersionID(3), CreatedAt: daysAgo(now, 0), Slug: "app", Kind: "external_change", Actor: "unknown", Files: []historyFile{f4}},
	})
	require.NoError(t, RetentionSweep(historyRoot, 90, false, now))
	ids = manifestIDs(t, historyRoot, "app")
	assert.NotContains(t, ids, fixtureVersionID(1), "second sweep asserts RULE 1: V1 removed (successor >90d)")
	assert.Contains(t, ids, fixtureVersionID(2), "V2 kept (successor V3 is today)")
	assert.Contains(t, ids, fixtureVersionID(3), "V3 kept (successor V4 is today)")
	assert.Contains(t, ids, fixtureVersionID(4), "V4 newest — never removed")
	_, err := os.Stat(filepath.Join(blobs, f1.SHA256))
	assert.True(t, os.IsNotExist(err), "the blob only V1 referenced is removed by rule 3")
	for _, keep := range []historyFile{f2, f4} {
		assert.FileExists(t, filepath.Join(blobs, keep.SHA256), "blobs retained manifests reference are kept")
	}
}

// TestRetention_RetainedManifestsUntouched_AfterPrune (S-5.15). BDD: rule 1
// prunes V1; retained V2's manifest file is byte-identical before and after
// the sweep — the sweep removes manifests and unreferenced blobs and rewrites
// nothing it keeps (paths/paths_total durable).
// Traces to: skills-spec-r15.md §9.1 rule 3 scope, S-5.15.
func TestRetention_RetainedManifestsUntouched_AfterPrune(t *testing.T) {
	now := time.Now().UTC()
	historyRoot := t.TempDir()
	f2 := mkFile("SKILL.md", 96)
	seedHistory(t, historyRoot, "app", []historyEntry{
		{ID: fixtureVersionID(1), CreatedAt: daysAgo(now, 200), Slug: "app", Kind: "edit", Actor: "agent",
			Files: []historyFile{mkFile("SKILL.md", 64)}, Paths: []string{"SKILL.md"}, PathsTotal: 1},
		{ID: fixtureVersionID(2), Parent: fixtureVersionID(1), CreatedAt: daysAgo(now, 100), Slug: "app", Kind: "edit", Actor: "agent",
			Files: []historyFile{f2}, Paths: []string{"SKILL.md", "reference/forms.md"}, PathsTotal: 2},
	})
	before := readFile(t, filepath.Join(historyRoot, "app", "versions", fixtureVersionID(2)+".json"))

	require.NoError(t, RetentionSweep(historyRoot, 90, false, now))

	after := readFile(t, filepath.Join(historyRoot, "app", "versions", fixtureVersionID(2)+".json"))
	assert.Equal(t, before, after,
		"the sweep never rewrites a retained manifest — its paths/paths_total are durable (S-5.15)")
	_, err := os.Stat(filepath.Join(historyRoot, "app", "versions", fixtureVersionID(1)+".json"))
	assert.True(t, os.IsNotExist(err), "V1 pruned by the same sweep")
}

// fixtureVersionID uses the normative v + six digits + UTC format. Test
// clocks are supplied by created_at; no reader relies on informal v1 names.
func fixtureVersionID(n int) string { return fmt.Sprintf("v%06d-20260101T000000Z", n) }

func seedPrunableControl(t *testing.T, historyRoot string, now time.Time) {
	t.Helper()
	seedHistory(t, historyRoot, "prunable-control", []historyEntry{
		{ID: fixtureVersionID(1), CreatedAt: daysAgo(now, 200), Kind: "edit", Actor: "person", Files: []historyFile{mkFile("old.txt", 60)}},
		{ID: fixtureVersionID(2), Parent: fixtureVersionID(1), CreatedAt: daysAgo(now, 100), Kind: "edit", Actor: "person", Files: []historyFile{mkFile("new.txt", 70)}},
	})
}
func assertPrunedControl(t *testing.T, historyRoot string) {
	t.Helper()
	assert.NotContains(t, manifestIDs(t, historyRoot, "prunable-control"), fixtureVersionID(1), "instrument control: this sweep really pruned an expired predecessor")
}

// S-5.13: test recoverability, not just retention of filenames. Versions and
// hashes are produced by the real pipeline. Only the clock is fixture input.
func TestRetention_RestoreRetainedState_ByteExact(t *testing.T) {
	root := t.TempDir()
	out, v1 := createSkillViaPipeline(t, root, "app", "retained", "recover me")
	hist := SkillHistoryRoot(root)
	_, err := ApplyChange(root, ChangeRequest{Kind: KindDelete, Slug: "app", Expect: Expectation{Revision: out.Revision}, Actor: agentActor("agent-a")})
	require.NoError(t, err)
	now := time.Now().UTC()
	p := filepath.Join(hist, "app", "versions", v1+".json")
	raw, err := os.ReadFile(p)
	require.NoError(t, err)
	var manifest map[string]any
	require.NoError(t, json.Unmarshal(raw, &manifest))
	manifest["created_at"] = daysAgo(now, 100)
	raw, err = json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(p, raw, 0o644))
	seedPrunableControl(t, hist, now)
	require.NoError(t, RetentionSweep(hist, 90, false, now))
	assertPrunedControl(t, hist)
	_, err = ApplyChange(root, ChangeRequest{Kind: KindRestore, Slug: "app", Version: v1, Expect: Expectation{Absent: true}, Actor: agentActor("agent-a")})
	require.NoError(t, err, "old pre-deletion state stays actually restorable")
	assert.Equal(t, validSkillMD("app", "retained", "recover me"), readFile(t, filepath.Join(root, "app", "SKILL.md")))
}
