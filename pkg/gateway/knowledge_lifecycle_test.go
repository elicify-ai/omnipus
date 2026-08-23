// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for the ADR-067 W3 indexing lifecycle (knowledge_lifecycle.go):
// FR-030, FR-031, FR-032, FR-033, FR-034a, FR-036, FR-038a, FR-039, FR-080,
// FR-110, FR-111 and US-16 AS-2/AS-4.
//
// Every expected value below is derived from the spec or from the fixture the
// test builds — never from what the implementation happens to produce. Where a
// number could be read off the code (the file total, the index directory), the
// test builds the fixture itself and counts what it made.

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/knowledge"
	"github.com/elicify-ai/omnipus/pkg/workspace"
)

// --- fixtures --------------------------------------------------------------

// kltFrames is a thread-safe recorder for emitted progress frames.
type kltFrames struct {
	mu     sync.Mutex
	frames []gen.KnowledgeIndexProgressFrame
}

func (r *kltFrames) emit(f gen.KnowledgeIndexProgressFrame) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.frames = append(r.frames, f)
}

func (r *kltFrames) all() []gen.KnowledgeIndexProgressFrame {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]gen.KnowledgeIndexProgressFrame, len(r.frames))
	copy(out, r.frames)
	return out
}

func (r *kltFrames) phases() []string {
	all := r.all()
	out := make([]string, 0, len(all))
	for _, f := range all {
		out = append(out, f.Phase)
	}
	return out
}

// kltVault builds a knowledge base: the Omnipus marker (FR-020) plus the files
// named. It returns the REAL path, because that is the identity FR-031 keys on
// and macOS temp dirs are symlinks.
func kltVault(t *testing.T, files map[string]string) string {
	t.Helper()
	root := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(root, knowledge.MarkerDirName), 0o700))
	for name, body := range files {
		full := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o600))
	}
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	return realRoot
}

// kltHome is a temp $OMNIPUS_HOME, as a real path.
func kltHome(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(home)
	require.NoError(t, err)
	return realRoot
}

// kltTree lists every path under root, relative and sorted. Used to prove the
// index never lands inside the operator's collection (FR-030).
func kltTree(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.Walk(root, func(p string, _ os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(root, p)
		if relErr != nil {
			return relErr
		}
		out = append(out, filepath.ToSlash(rel))
		return nil
	}))
	sort.Strings(out)
	return out
}

// kltLifecycle builds a lifecycle and guarantees it is stopped, so no drift
// goroutine outlives the test.
func kltLifecycle(t *testing.T, opts KnowledgeLifecycleOptions) *KnowledgeLifecycle {
	t.Helper()
	kl, err := NewKnowledgeLifecycle(opts)
	require.NoError(t, err)
	t.Cleanup(kl.Stop)
	return kl
}

// --- FR-036 / FR-080: the frame cannot state a total it does not have ------

func TestKnowledgeIndexProgressFrame_TotalAbsentUntilTotalKnown(t *testing.T) {
	now := time.Date(2026, 8, 21, 10, 0, 0, 0, time.UTC)

	t.Run("enumerating never carries a total, even if one is offered", func(t *testing.T) {
		// The caller mistakenly supplies a total alongside the enumerating
		// phase. FR-036: the frame must still be indeterminate.
		f := newKnowledgeIndexProgressFrame("kb1", "ws1", knowledgeIndexUpdate{
			Phase:      knowledgeIndexPhaseEnumerating,
			Total:      999,
			TotalKnown: true,
		}, now)

		assert.Equal(t, "enumerating", f.Phase)
		assert.False(t, f.TotalKnown, "total_known must be false while enumerating (FR-036)")
		assert.Nil(t, f.TotalFiles, "total_files must be ABSENT while enumerating (FR-036)")

		// And absent on the wire, not merely zero: a client reading
		// `total_files ?? 0` would compute a ratio against a fabricated total.
		raw, err := json.Marshal(f)
		require.NoError(t, err)
		var wire map[string]any
		require.NoError(t, json.Unmarshal(raw, &wire))
		_, present := wire["total_files"]
		assert.False(t, present, "total_files key must not appear on the wire: %s", raw)
		assert.Equal(t, false, wire["total_known"])
		assert.Equal(t, "knowledge_index_progress", wire["type"],
			"discriminator is fixed by contracts/asyncapi.yaml")
	})

	t.Run("indexing carries the measured total", func(t *testing.T) {
		f := newKnowledgeIndexProgressFrame("kb1", "ws1", knowledgeIndexUpdate{
			Phase:      knowledgeIndexPhaseIndexing,
			Indexed:    7,
			Total:      120,
			TotalKnown: true,
		}, now)

		assert.True(t, f.TotalKnown)
		require.NotNil(t, f.TotalFiles, "total_files must be present once total_known is true")
		assert.Equal(t, int64(120), *f.TotalFiles)
		assert.Equal(t, int64(7), f.IndexedFiles)
	})

	t.Run("a known total of zero is not a denominator", func(t *testing.T) {
		// IndexProgress.Ratio refuses total <= 0 so "0 of 0" cannot be rendered.
		// The frame must refuse it for the same reason.
		f := newKnowledgeIndexProgressFrame("kb1", "ws1", knowledgeIndexUpdate{
			Phase:      knowledgeIndexPhaseIndexing,
			Total:      0,
			TotalKnown: true,
		}, now)

		assert.False(t, f.TotalKnown)
		assert.Nil(t, f.TotalFiles)
	})

	t.Run("a failure is terminal and named", func(t *testing.T) {
		f := newKnowledgeIndexProgressFrame("kb1", "ws1", knowledgeIndexUpdate{
			Phase: knowledgeIndexPhaseIdle,
			Err:   errors.New("disk on fire"),
		}, now)

		assert.Equal(t, "failed", f.Phase, "an error must not be reported as idle")
		require.NotNil(t, f.Error)
		assert.Contains(t, *f.Error, "disk on fire")
	})
}

// --- FR-030 / FR-039 / FR-080: attach indexes and reports honestly ---------

func TestKnowledgeLifecycle_AttachIndexesCollectionAndReportsProgress(t *testing.T) {
	// Fixture: four indexable files (the marker directory is collection
	// configuration, not content, and is not indexable).
	root := kltVault(t, map[string]string{
		"one.md":            "# One\nalpha",
		"two.md":            "# Two\nbeta",
		"notes/three.md":    "# Three\ngamma",
		"notes/diagram.png": "not-read",
	})
	const wantTotal = 4

	home := kltHome(t)
	rec := &kltFrames{}
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home, Emit: rec.emit})

	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))

	frames := rec.all()
	require.GreaterOrEqual(t, len(frames), 2,
		"a first index must announce itself and announce its end")

	// The first frame is indeterminate: the client must render an
	// indeterminate state and must never compute a ratio (FR-036).
	first := frames[0]
	assert.Equal(t, "enumerating", first.Phase)
	assert.False(t, first.TotalKnown)
	assert.Nil(t, first.TotalFiles)

	// Somewhere between, a realRoot measured total appears.
	var sawIndexing bool
	for _, f := range frames {
		if f.Phase != "indexing" {
			continue
		}
		sawIndexing = true
		require.NotNil(t, f.TotalFiles)
		assert.Equal(t, int64(wantTotal), *f.TotalFiles,
			"the total is the count of indexable files in the fixture")
		assert.True(t, f.TotalKnown)
	}
	assert.True(t, sawIndexing, "a first index must publish its measured total")

	// The terminal frame is the success state, with everything accounted for.
	last := frames[len(frames)-1]
	assert.Equal(t, "idle", last.Phase, "phases seen: %v", rec.phases())
	assert.Nil(t, last.Error)
	assert.True(t, last.TotalKnown)
	require.NotNil(t, last.TotalFiles)
	assert.Equal(t, int64(wantTotal), *last.TotalFiles)
	assert.Equal(t, int64(wantTotal), last.IndexedFiles)
	require.NotNil(t, last.SkippedFiles)
	assert.Equal(t, int64(0), *last.SkippedFiles)

	// Every frame names the same collection and the workspace it came through.
	for _, f := range frames {
		assert.Equal(t, "ws-a", f.WorkspaceId)
		assert.NotEmpty(t, f.CollectionId)
		assert.Equal(t, frames[0].CollectionId, f.CollectionId)
		require.NotNil(t, f.UpdatedAt)
	}

	// The content is actually findable — the whole point of the wiring.
	ix, ok := kl.IndexForRoot(root)
	require.True(t, ok)
	hits, err := ix.Search("gamma", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1)
	assert.Equal(t, "notes/three.md", hits[0].Path)
}

// --- FR-030: the index lives under $OMNIPUS_HOME, never in the vault -------

func TestKnowledgeLifecycle_IndexLivesUnderHomeNeverInTheCollection(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	before := kltTree(t, root)

	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home})
	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))

	after := kltTree(t, root)
	assert.Equal(t, before, after,
		"indexing must not write a single byte into the operator's collection (FR-030)")

	indexDir, err := knowledge.IndexDirFor(home, root)
	require.NoError(t, err)
	assert.True(t, strings.HasPrefix(indexDir, home+string(os.PathSeparator)),
		"index dir %q must be under $OMNIPUS_HOME %q", indexDir, home)
	assert.False(t, strings.HasPrefix(indexDir, root+string(os.PathSeparator)),
		"index dir %q must never be inside the collection %q", indexDir, root)
	st, err := os.Stat(indexDir)
	require.NoError(t, err, "the index directory must exist after an attach")
	assert.True(t, st.IsDir())

	// FR-032: index directories are 0700.
	assert.Equal(t, os.FileMode(0o700), st.Mode().Perm())
}

// --- FR-031 / US-16 AS-2: one folder, one index, reference counted ---------

func TestKnowledgeLifecycle_SharedIndexAcrossMountsClosesOnLastRelease(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	var opens, scans, syncs int
	var mu sync.Mutex
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		OpenIndex: func(h, r string) (*knowledge.Index, error) {
			mu.Lock()
			opens++
			mu.Unlock()
			return knowledge.OpenIndex(h, r)
		},
		Scan: func(r string) (*knowledge.ScanResult, error) {
			mu.Lock()
			scans++
			mu.Unlock()
			return knowledge.Scan(r)
		},
		SyncWith: func(ctx context.Context, ix *knowledge.Index, o knowledge.SyncOptions) (knowledge.SyncStats, error) {
			mu.Lock()
			syncs++
			mu.Unlock()
			return ix.SyncWith(ctx, o)
		},
	})

	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))
	// Second workspace, same folder, reached by a DIFFERENT spelling — a
	// trailing separator. FR-031 keys on the resolved real path, so this is the
	// same collection.
	require.NoError(t, kl.AttachMount(context.Background(), "ws-b", "kb", root+string(os.PathSeparator)))

	assert.Equal(t, []string{root}, kl.AttachedRoots(), "two mounts, one collection")
	assert.Equal(t, 2, kl.HoldersFor(root))

	mu.Lock()
	gotOpens, gotScans, gotSyncs := opens, scans, syncs
	mu.Unlock()
	assert.Equal(t, 2, gotOpens, "one OpenIndex per mount — pkg/knowledge counts the references")
	assert.Equal(t, 1, gotScans, "one folder is enumerated once, not once per mount")
	assert.Equal(t, 1, gotSyncs, "one folder is indexed once, not once per mount")

	ixA, ok := kl.IndexForRoot(root)
	require.True(t, ok)

	// Revoking ONE of two mounts must leave the other workspace's search working.
	require.NoError(t, kl.RevokeMount("ws-a", "vault"))
	assert.Equal(t, 1, kl.HoldersFor(root))
	_, err := ixA.DocCount()
	require.NoError(t, err, "the surviving mount must still be able to search (US-16 AS-2)")

	// The last release closes it.
	require.NoError(t, kl.RevokeMount("ws-b", "kb"))
	assert.Empty(t, kl.AttachedRoots())
	_, err = ixA.DocCount()
	require.Error(t, err, "the last release must close the index handle (FR-031)")
}

// A collection mounted into a SECOND workspace while its first index is still
// running must have that index's progress reported to BOTH workspaces. One
// index, one indexing run, but two readers waiting to hear about it.
func TestKnowledgeLifecycle_ProgressReachesEveryWorkspaceMountingTheCollection(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	release := make(chan struct{})
	entered := make(chan struct{})
	rec := &kltFrames{}
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		Emit: rec.emit,
		SyncWith: func(ctx context.Context, ix *knowledge.Index, o knowledge.SyncOptions) (knowledge.SyncStats, error) {
			close(entered)
			<-release
			return ix.SyncWith(ctx, o)
		},
	})

	done := make(chan error, 1)
	go func() { done <- kl.AttachMount(context.Background(), "ws-a", "vault", root) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("indexing never started")
	}

	// Second workspace joins mid-index. It must not start a second index...
	require.NoError(t, kl.AttachMount(context.Background(), "ws-b", "kb", root))
	assert.Equal(t, 2, kl.HoldersFor(root))

	close(release)
	require.NoError(t, <-done)

	// ...and it must still be told when the index finished.
	byWorkspace := map[string]string{}
	for _, f := range rec.all() {
		if f.Phase == "idle" {
			byWorkspace[f.WorkspaceId] = f.CollectionId
		}
	}
	assert.Contains(t, byWorkspace, "ws-a")
	assert.Contains(t, byWorkspace, "ws-b",
		"a workspace that joined mid-index must still receive the terminal frame")
	assert.Equal(t, byWorkspace["ws-a"], byWorkspace["ws-b"],
		"one folder is one collection_id, whichever workspace it is seen through (FR-031)")
}

// --- FR-110 / FR-111 / US-16 AS-4: a broken mount degrades honestly --------

func TestKnowledgeLifecycle_BrokenMountIsReportedNotSilentlyEmpty(t *testing.T) {
	home := kltHome(t)
	rec := &kltFrames{}
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home, Emit: rec.emit})

	t.Run("a target that never existed", func(t *testing.T) {
		missing := filepath.Join(t.TempDir(), "gone")
		err := kl.AttachMount(context.Background(), "ws-a", "vault", missing)
		require.Error(t, err)
		assert.ErrorIs(t, err, ErrKnowledgeMountUnreachable)
		assert.Empty(t, kl.AttachedRoots(), "a broken mount must attach no index at all")
		assert.Empty(t, rec.all(), "a broken mount must not report indexing progress")
	})

	t.Run("a collection whose folder was moved away", func(t *testing.T) {
		root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
		require.NoError(t, kl.AttachMount(context.Background(), "ws-b", "vault", root))
		require.NoError(t, kl.RevokeMount("ws-b", "vault"))

		moved := root + "-moved"
		require.NoError(t, os.Rename(root, moved))
		t.Cleanup(func() { _ = os.Rename(moved, root) })

		err := kl.AttachMount(context.Background(), "ws-b", "vault", root)
		require.Error(t, err, "a moved folder must surface as broken, not as an empty collection")
		assert.ErrorIs(t, err, ErrKnowledgeMountUnreachable)
	})
}

// --- US-4 AS-3: a folder with no marker gets no knowledge-base features ----

func TestKnowledgeLifecycle_OrdinaryFolderIsNotIndexed(t *testing.T) {
	plain := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(plain, "notes.md"), []byte("# hi"), 0o600))

	home := kltHome(t)
	rec := &kltFrames{}
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home, Emit: rec.emit})

	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "docs", plain),
		"an ordinary folder is not an error")
	assert.Empty(t, kl.AttachedRoots(), "an ordinary folder must not get an index")
	assert.Empty(t, rec.all(), "an ordinary folder must not report indexing progress")

	entries, err := os.ReadDir(filepath.Join(home, "knowledge"))
	if err == nil {
		assert.Empty(t, entries, "no index directory may be created for an ordinary folder")
	} else {
		assert.True(t, errors.Is(err, os.ErrNotExist), "unexpected error: %v", err)
	}
}

// --- FR-038a: the drift check runs on a schedule, reports only failures ----

func TestKnowledgeLifecycle_DriftCheckRunsOnMountAndOnScheduleWithNoButton(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	ticks := make(chan time.Time)
	var mu sync.Mutex
	var runs int
	var notified []knowledge.DriftReport

	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		DriftCheck: func(_ context.Context, ix *knowledge.Index) (knowledge.DriftReport, error) {
			mu.Lock()
			runs++
			mu.Unlock()
			return knowledge.DriftReport{Root: ix.Root(), CheckedAt: time.Now()}, nil // healthy
		},
		DriftNotify: func(r knowledge.DriftReport) {
			mu.Lock()
			notified = append(notified, r)
			mu.Unlock()
		},
		NewTicker: func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} },
	})

	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))
	// Mount the SAME folder a second time. FR-038a allows at most one run per
	// collection in flight, so a second mount must not double the schedule.
	require.NoError(t, kl.AttachMount(context.Background(), "ws-b", "kb", root))

	// "Plus once on mount": the first run happens with no tick at all.
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return runs >= 1
	}, 2*time.Second, 5*time.Millisecond, "the check must run once on mount")

	// Then one run per tick, and exactly one — not one per mount.
	for i := 0; i < 3; i++ {
		ticks <- time.Now()
	}
	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return runs == 4
	}, 2*time.Second, 5*time.Millisecond, "3 ticks after the mount run must produce exactly 4 runs")

	mu.Lock()
	gotNotified := len(notified)
	mu.Unlock()
	assert.Equal(t, 0, gotNotified,
		"a healthy collection must produce no notification, however often it is checked")
}

func TestKnowledgeLifecycle_DriftCheckReportsOnlyWhenSomethingIsWrong(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	var mu sync.Mutex
	var notified []knowledge.DriftReport

	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		DriftCheck: func(_ context.Context, ix *knowledge.Index) (knowledge.DriftReport, error) {
			return knowledge.DriftReport{
				Root:     ix.Root(),
				Findings: []knowledge.DriftFinding{{Kind: knowledge.DriftMissingFromDisk, Path: "a.md"}},
			}, nil
		},
		DriftNotify: func(r knowledge.DriftReport) {
			mu.Lock()
			notified = append(notified, r)
			mu.Unlock()
		},
		NewTicker: func(time.Duration) (<-chan time.Time, func()) {
			return make(chan time.Time), func() {}
		},
	})

	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(notified) == 1
	}, 2*time.Second, 5*time.Millisecond, "one unhealthy run must produce exactly one report")

	mu.Lock()
	got := notified[0]
	mu.Unlock()
	assert.Equal(t, root, got.Root)
	assert.False(t, got.Healthy())
	assert.Contains(t, got.Summary(), "drifted from its index")
}

func TestKnowledgeLifecycle_DriftIntervalDefaultsToSixHoursAndIsConfigurable(t *testing.T) {
	home := kltHome(t)

	// FR-038a: "every 6 hours by default".
	def := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home})
	assert.Equal(t, 6*time.Hour, def.DriftInterval())
	assert.Equal(t, knowledge.DefaultDriftInterval, def.DriftInterval())

	// "...operator-configurable".
	custom := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home, DriftInterval: 90 * time.Minute})
	assert.Equal(t, 90*time.Minute, custom.DriftInterval())
}

// --- US-6 (P0): a search issued during the first index is not "complete" ---

func TestKnowledgeLifecycle_SearchDuringFirstIndexIsNotReportedComplete(t *testing.T) {
	root := kltVault(t, map[string]string{
		"a.md": "# A\nalpha", "b.md": "# B\nbeta", "c.md": "# C\ngamma",
	})
	const wantTotal = 3
	home := kltHome(t)

	release := make(chan struct{})
	entered := make(chan struct{})
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		SyncWith: func(ctx context.Context, ix *knowledge.Index, o knowledge.SyncOptions) (knowledge.SyncStats, error) {
			close(entered)
			<-release
			return ix.SyncWith(ctx, o)
		},
	})

	done := make(chan error, 1)
	go func() { done <- kl.AttachMount(context.Background(), "ws-a", "vault", root) }()

	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("indexing never started")
	}

	// The lifecycle must have driven the SHARED tracker — the one the
	// knowledge_search tool reads — or every mid-index answer would be
	// reported as the whole truth (SharedProgressTracker's wiring obligation).
	progress := knowledge.SharedProgressTracker(root).Progress()
	assert.True(t, progress.InFlight(), "an index in flight must be visible to a searcher")
	indexed, total, ok := progress.Ratio()
	require.True(t, ok, "a first index publishes a measured total")
	assert.Equal(t, 0, indexed)
	assert.Equal(t, wantTotal, total)

	ix, found := kl.IndexForRoot(root)
	require.True(t, found)
	searcher, err := knowledge.NewSearcher(ix, knowledge.SharedProgressTracker(root))
	require.NoError(t, err)
	resp, err := searcher.Search("alpha", knowledge.SearchOptions{})
	require.NoError(t, err)
	_, report := resp.Results()
	assert.False(t, report.Complete, "a search during a first index must not claim completeness")
	assert.Contains(t, report.Statement, "incomplete")

	close(release)
	require.NoError(t, <-done)

	// And once it finishes, the incompleteness notice disappears (US-6 AS-4).
	resp, err = searcher.Search("alpha", knowledge.SearchOptions{})
	require.NoError(t, err)
	_, report = resp.Results()
	assert.True(t, report.Complete)
	assert.Empty(t, report.Statement)
}

// --- FR-033 / FR-039: a reopen reconciles, it does not rebuild -------------

func TestKnowledgeLifecycle_ReopenReconcilesWithoutRebuilding(t *testing.T) {
	// The oracle here is deliberately one a REBUILD cannot pass.
	//
	// FR-033: only a file whose recorded size, modification time or content
	// hash changed is re-parsed. So a note edited in place to the SAME BYTE
	// LENGTH, with its modification time restored, is by construction NOT
	// re-parsed on reopen — and the index still answers with the OLD word.
	// A rebuild would answer with the new one. "No error" and "the same
	// counts" are both passable by a full rebuild; this is not.
	const before = "# A\nalpha"
	const after = "# A\nomega" // identical length, so stat cannot tell them apart
	require.Equal(t, len(before), len(after), "the fixture only works at equal length")

	root := kltVault(t, map[string]string{"a.md": before, "b.md": "# B\nbeta"})
	const wantTotal = 2
	home := kltHome(t)

	var mu sync.Mutex
	var scans int
	var stats []knowledge.SyncStats
	rec := &kltFrames{}

	newKL := func() *KnowledgeLifecycle {
		return kltLifecycle(t, KnowledgeLifecycleOptions{
			Home: home,
			Emit: rec.emit,
			Scan: func(r string) (*knowledge.ScanResult, error) {
				mu.Lock()
				scans++
				mu.Unlock()
				return knowledge.Scan(r)
			},
			SyncWith: func(ctx context.Context, ix *knowledge.Index, o knowledge.SyncOptions) (knowledge.SyncStats, error) {
				s, err := ix.SyncWith(ctx, o)
				mu.Lock()
				stats = append(stats, s)
				mu.Unlock()
				return s, err
			},
		})
	}

	first := newKL()
	require.NoError(t, first.AttachMount(context.Background(), "ws-a", "vault", root))

	mu.Lock()
	require.Len(t, stats, 1, "the first index runs through the measured-total path")
	firstRun := stats[0]
	mu.Unlock()
	assert.Equal(t, wantTotal, firstRun.Indexed, "the first index parses every file")
	assert.Equal(t, 0, firstRun.Unchanged)

	require.NoError(t, first.RevokeMount("ws-a", "vault"))

	// Edit a.md in place, same length, same modification time.
	notePath := filepath.Join(root, "a.md")
	info, err := os.Stat(notePath)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(notePath, []byte(after), 0o600))
	require.NoError(t, os.Chtimes(notePath, info.ModTime(), info.ModTime()))

	framesBeforeReopen := len(rec.all())

	// A second gateway lifetime over the same $OMNIPUS_HOME.
	second := newKL()
	require.NoError(t, second.AttachMount(context.Background(), "ws-a", "vault", root))

	ix, ok := second.IndexForRoot(root)
	require.True(t, ok)

	hits, err := ix.Search("alpha", 10)
	require.NoError(t, err)
	require.Len(t, hits, 1,
		"the reopened index still holds the ORIGINAL parse — nothing was rebuilt (FR-033/FR-039)")
	assert.Equal(t, "a.md", hits[0].Path)

	hits, err = ix.Search("omega", 10)
	require.NoError(t, err)
	assert.Empty(t, hits, "a reopen that re-parsed an unchanged-by-stat file would find the new word")

	mu.Lock()
	gotScans := scans
	mu.Unlock()
	assert.Equal(t, 1, gotScans,
		"only a FIRST index pays for the extra enumeration walk; a reconcile must not (MV-4)")

	// A reconcile is honestly indeterminate: it publishes no measured total,
	// because it never measured one (FR-036).
	reopenFrames := rec.all()[framesBeforeReopen:]
	require.NotEmpty(t, reopenFrames)
	for _, f := range reopenFrames {
		assert.NotEqual(t, "indexing", f.Phase,
			"a reconcile must not claim a total it did not measure")
	}
	last := reopenFrames[len(reopenFrames)-1]
	assert.Equal(t, "idle", last.Phase)
	require.NotNil(t, last.TotalFiles)
	assert.Equal(t, int64(wantTotal), *last.TotalFiles,
		"the terminal frame reports the collection it actually reconciled")
}

// --- mount handlers reach the lifecycle -----------------------------------

func TestKnowledgeLifecycle_RegistryResolvesByHomeAndToleratesAbsence(t *testing.T) {
	home := kltHome(t)

	// A gateway with no knowledge lifecycle must not crash the mount handlers.
	api := &restAPI{homePath: home}
	assert.Nil(t, api.knowledgeLifecycle())
	assert.NotPanics(t, func() { api.knowledgeLifecycle().AttachMountAsync("ws", "m", home) })
	assert.NotPanics(t, func() {
		require.NoError(t, api.knowledgeLifecycle().RevokeMount("ws", "m"))
	})

	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home})
	registerKnowledgeLifecycle(home, kl)
	t.Cleanup(func() { unregisterKnowledgeLifecycle(home) })

	assert.Same(t, kl, api.knowledgeLifecycle())
	assert.Nil(t, (&restAPI{homePath: kltHome(t)}).knowledgeLifecycle(),
		"a different $OMNIPUS_HOME must not see this lifecycle")
}

// --- FR-039: boot reattaches every mount already on disk -------------------

func TestKnowledgeLifecycle_AttachAllMountsReopensRecordedMounts(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	// The mount record is written by the REAL store, workspace.CreateMount,
	// not hand-marshalled here.
	//
	// This test used to fabricate the JSON — {"workspace_id":…,"mounts":[…]} —
	// straight into $HOME/entities/mounts/. It passed, but only because the
	// fabricated shape happened to coincide with the real one. The production
	// reader is workspace.LoadMounts over workspace.Mount, so a field added,
	// renamed or made required by the store's WRITER would leave this test
	// green against its own private file while boot reattached nothing at all
	// — the exact failure it exists to catch. An oracle has to be produced by
	// the API under test, never re-derived beside it.
	const bootWS = "wsboot"
	kwWorkspace(t, home, bootWS)
	_, _, err := workspace.CreateMount(home, bootWS, "vault", root)
	require.NoError(t, err)

	rec := &kltFrames{}
	kl := kltLifecycle(t, KnowledgeLifecycleOptions{Home: home, Emit: rec.emit})
	kl.AttachAllMounts()
	kl.WaitForAttaches()

	assert.Equal(t, []string{root}, kl.AttachedRoots(),
		"a recorded mount must be reopened at boot (FR-039)")
	frames := rec.all()
	require.NotEmpty(t, frames)
	assert.Equal(t, bootWS, frames[0].WorkspaceId)
}

// --- FR-038a read to its end: DETECTING drift and repairing nothing --------

// TestKnowledgeLifecycle_DriftRepairsTheIndexItFoundStale is the regression for
// the defect that made the whole drift lane cosmetic.
//
// # What was wrong, stated as a user would hit it
//
// A mounted knowledge base was indexed EXACTLY ONCE — at boot, or at
// mount-create — and never again for the life of the process. Write a new note
// into your vault and knowledge_search could not find it until you restarted
// the gateway. Reproduced live before this fix: mount a vault, search
// "brontosaurus" → 0 hits; write a note containing "brontosaurus" into the
// mounted folder; re-search immediately and again a minute later → still 0
// hits, and still `"complete": true, "statement": "Searched the whole of this
// knowledge base; its index was complete at query time."`
//
// The drift check noticed. It reported to a log line and changed nothing.
//
// # Why "complete: true" made it invisible
//
// SearchReport.Complete is derived from the progress tracker
// (`Complete: !p.InFlight()`), which is idle for a stale index exactly as it is
// for a fresh one. So "complete" meant "no indexing run is in flight" while
// reading as "these results are all of them" — a confidently incomplete
// answer, which is the single failure US-6 (a P0) exists to make impossible.
//
// # The oracle
//
// A note written to disk AFTER the first index becomes findable, with no
// restart and no second mount, once one drift tick has fired. That is
// something the pre-fix code cannot pass by any route: the only other thing
// that re-indexes is a fresh process.
//
// The drift CHECK is the real knowledge.CheckDrift — not a stub — so the test
// also proves the check genuinely reports a note added behind the index's back.
// Only the clock is injected, which is FR-038a's own test rule ("count runs
// against injected ticks, never sleep").
//
// DIES ON: removing the kl.resyncAfterDrift(r) call from the notify wrapper in
// NewKnowledgeLifecycle, and on resyncAfterDrift returning early.
func TestKnowledgeLifecycle_DriftRepairsTheIndexItFoundStale(t *testing.T) {
	root := kltVault(t, map[string]string{"a.md": "# A\nalpha"})
	home := kltHome(t)

	ticks := make(chan time.Time)
	var mu sync.Mutex
	var notified []knowledge.DriftReport

	kl := kltLifecycle(t, KnowledgeLifecycleOptions{
		Home: home,
		DriftNotify: func(r knowledge.DriftReport) {
			mu.Lock()
			notified = append(notified, r)
			mu.Unlock()
		},
		NewTicker: func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} },
	})

	require.NoError(t, kl.AttachMount(context.Background(), "ws-a", "vault", root))

	ix, found := kl.IndexForRoot(root)
	require.True(t, found)
	searcher, err := knowledge.NewSearcher(ix, knowledge.SharedProgressTracker(root))
	require.NoError(t, err)

	// Anti-vacuity: the word must genuinely be absent first, or "found after
	// the tick" would pass against a search that matches everything.
	resp, err := searcher.Search("brontosaurus", knowledge.SearchOptions{})
	require.NoError(t, err)
	hits, report := resp.Results()
	require.Empty(t, hits, "the fixture must not contain the word before it is written")
	require.True(t, report.Complete,
		"the pre-write index is genuinely complete — this records the state the stale "+
			"index was indistinguishable from")

	// The operator writes a note into their own folder, behind the index's back.
	require.NoError(t, os.WriteFile(filepath.Join(root, "new-note.md"),
		[]byte("# New note\nbrontosaurus"), 0o600))

	// One scheduled drift tick. No restart, no re-mount, no button (FR-038a).
	ticks <- time.Now()

	require.Eventually(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(notified) >= 1
	}, 5*time.Second, 10*time.Millisecond,
		"the real CheckDrift must notice a note added behind the index's back")

	// The repair runs asynchronously off the report — deliberately, so a large
	// re-index does not hold the per-collection drift goroutine — so poll the
	// index rather than assuming the report and the repair are one step.
	// The ORACLE is unchanged: the note becomes findable with no restart.
	require.Eventuallyf(t, func() bool {
		r, sErr := searcher.Search("brontosaurus", knowledge.SearchOptions{})
		if sErr != nil {
			return false
		}
		h, rep := r.Results()
		// Both halves, together: the note is found AND the run that found it
		// has finished, so the completeness statement is the settled one.
		return len(h) == 1 && rep.Complete
	}, 10*time.Second, 25*time.Millisecond,
		"the drift check found the index stale and must REPAIR it, not merely report it. "+
			"Never finding the note means a note written into a mounted vault stays "+
			"unfindable until the gateway restarts, while knowledge_search keeps answering "+
			"that it searched the whole collection")

	resp, err = searcher.Search("brontosaurus", knowledge.SearchOptions{})
	require.NoError(t, err)
	hits, report = resp.Results()
	require.Len(t, hits, 1)
	assert.Equal(t, "new-note.md", hits[0].Path)
	assert.True(t, report.Complete,
		"after the repair settles the index really is complete, and may say so")
	assert.Empty(t, report.Statement,
		"a settled, repaired index shows no incompleteness statement (US-6 AS-4)")

	mu.Lock()
	firstReport := notified[0]
	mu.Unlock()
	assert.False(t, firstReport.Healthy(),
		"the operator's own notify hook must still see the UNHEALTHY report that triggered "+
			"the repair — repairing silently and reporting nothing would hide a vault that "+
			"drifts every hour")
}
