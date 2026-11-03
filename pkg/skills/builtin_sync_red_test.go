package skills

// RED tests — landing/skills (T9): built-ins read-only, synced at start,
// retired (spec §8, test rows 22–23). Scenarios S-7.1, S-7.2, S-7.4, S-7.5,
// S-7.10, S-7.6–S-7.9; dataset D-6 (start-up matrix).
// Traces to: skills-spec-r15.md §8.1–§8.5, §16 US-7, §17.1 rows 22/23,
// §17.2 D-6.
//
// RED expectation: SyncBuiltins / RetireBuiltins / RetiredBuiltinNames /
// IsBuiltin are RED scaffolds — every behavioural assertion below fails on
// the baseline (SeedDefaults never overwrites, never retires, no report).

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// exportShipped exports the embedded shipped package for slug into dest.
func exportShipped(t *testing.T, slug, dest string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o755))
	require.NoError(t, ExportEmbeddedPackage(slug, dest), "export embedded %s", slug)
}

// requireSameTree asserts dir matches a freshly exported shipped package
// file-for-file, byte-for-byte, with no extra files beyond Omnipus's own
// stamps — the positive content oracle for "replaced/installed" cases (a
// no-op that only touches stamps must fail here).
func requireSameTree(t *testing.T, slug, dir string) {
	t.Helper()
	shipped := filepath.Join(t.TempDir(), "shipped-"+slug)
	exportShipped(t, slug, shipped)

	shippedFiles := 0
	require.NoError(t, filepath.WalkDir(shipped, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(shipped, p)
		if err != nil {
			return err
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		got, readErr := os.ReadFile(filepath.Join(dir, rel))
		require.NoError(t, readErr, "installed tree carries shipped file %s", rel)
		assert.Equal(t, string(raw), string(got), "installed %s is byte-identical to shipped", rel)
		shippedFiles++
		return nil
	}), "the shipped export walks")
	require.Greater(t, shippedFiles, 0, "instrument check: the shipped package exported at least one file")

	require.NoError(t, filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(dir, p)
		if err != nil {
			return err
		}
		if rel == ".omnipus-builtin" || rel == SkillIdentityFile {
			return nil // Omnipus control metadata, never part of shipped
		}
		_, statErr := os.Stat(filepath.Join(shipped, rel))
		assert.NoError(t, statErr, "the installed folder carries no extra file beyond shipped: %s", rel)
		return nil
	}), "the installed tree walks")
}

// fileMtime returns the file's mtime.
func fileMtime(t *testing.T, path string) time.Time {
	t.Helper()
	info, err := os.Stat(path)
	require.NoError(t, err)
	return info.ModTime()
}

// seededGeneration is a fixture generation stamp value (UUID-format).
const seededGeneration = "11111111-2222-3333-4444-555555555555"

// seedGeneration writes a pre-existing generation stamp into a built-in
// folder fixture.
func seedGeneration(t *testing.T, dir string) {
	t.Helper()
	require.NoError(t, os.WriteFile(filepath.Join(dir, SkillIdentityFile),
		[]byte(seededGeneration+"\n"), 0o644))
}

// dirMtime returns the directory's mtime (untouched-folder oracle, S-7.1).
func dirMtime(t *testing.T, dir string) time.Time {
	t.Helper()
	info, err := os.Stat(dir)
	require.NoError(t, err)
	return info.ModTime()
}

// ageDir back-dates a directory so "mtime unchanged" is meaningful.
func ageDir(t *testing.T, dir string, at time.Time) {
	t.Helper()
	require.NoError(t, os.Chtimes(dir, at, at))
}

// seededGrants models the roster edge, with independent per-agent grant lists.
// Only RemoveGrant is exposed to production (§8.2); tests inspect final state.
type seededGrants struct {
	seeded  map[string][]string // agent id -> grant names
	removed []string
}

var _ GrantEditor = (*seededGrants)(nil)

func newSeededGrants(name string, agents ...string) *seededGrants {
	g := &seededGrants{seeded: map[string][]string{}}
	for _, agent := range agents {
		g.seeded[agent] = []string{name, "keep-" + agent}
	}
	return g
}

func (g *seededGrants) RemoveGrant(name string) error {
	g.removed = append(g.removed, strings.ToLower(name))
	for agent, names := range g.seeded {
		kept := make([]string, 0, len(names))
		for _, grant := range names {
			if !strings.EqualFold(grant, name) {
				kept = append(kept, grant)
			}
		}
		g.seeded[agent] = kept
	}
	return nil
}

// TestSyncBuiltins_StartupMatrix (S-7.1 + D-6 rows for a current built-in):
// stamped old content → replaced; stamped outside edit → replaced; missing →
// installed; stamped equal → untouched (mtime unchanged).
// Traces to: skills-spec-r15.md §8.2 case table, S-7.1, D-6.
func TestSyncBuiltins_StartupMatrix(t *testing.T) {
	cases := []struct {
		name       string
		setup      func(t *testing.T, root, planDir string)
		want       BuiltinSyncCase
		wantNew    bool // folder must end up byte-equal to shipped
		genKeepGen bool // a pre-existing generation stamp must survive
	}{
		{
			name: "stamped built-in with old content is replaced",
			setup: func(t *testing.T, root, planDir string) {
				writeSkillFixture(t, root, "plan") // fresh, not shipped
				builtinMarkerFixture(t, planDir)
			},
			want:    BuiltinUpdated,
			wantNew: true,
		},
		{
			name: "stamped built-in with an outside edit is replaced",
			setup: func(t *testing.T, root, planDir string) {
				exportShipped(t, "plan", planDir)
				builtinMarkerFixture(t, planDir)
				seedGeneration(t, planDir) // an existing generation stamp (§5.13a)
				require.NoError(t, os.WriteFile(filepath.Join(planDir, "SKILL.md"),
					[]byte("---\nname: plan\ndescription: edited outside\n---\noutside edit\n"), 0o644))
			},
			want:       BuiltinUpdated,
			wantNew:    true,
			genKeepGen: true, // the existing stamp is PRESERVED across the content update
		},
		{
			name:  "missing built-in is installed",
			setup: func(t *testing.T, root, planDir string) {},
			want:  BuiltinInstalled,
		},
		{
			name: "stamped equal built-in is untouched",
			setup: func(t *testing.T, root, planDir string) {
				exportShipped(t, "plan", planDir)
				builtinMarkerFixture(t, planDir)
				seedGeneration(t, planDir)
				ageDir(t, planDir, time.Now().Add(-time.Hour))
			},
			want:       BuiltinUnchanged,
			genKeepGen: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			planDir := filepath.Join(root, "plan")
			tc.setup(t, root, planDir)

			report, err := SyncBuiltins(root, nil)
			require.NoError(t, err, "SyncBuiltins must complete the §8.2 case work (RED: scaffold not implemented)")

			switch tc.want {
			case BuiltinUpdated:
				updated := findOutcome(report.Updated, "plan")
				require.NotNil(t, updated, "plan must appear in SyncReport.Updated (skill.builtin_updated source)")
			case BuiltinInstalled:
				assert.Contains(t, report.Installed, "plan", "missing built-in reported installed")
			case BuiltinUnchanged:
				assert.Nil(t, findOutcome(report.Updated, "plan"), "equal folder must not be reported updated")
				assert.NotContains(t, report.Installed, "plan")
			}

			// §8.2 r15: every installed or replaced built-in folder is
			// byte-equal to the shipped package (positive content oracle —
			// stamp-only no-ops fail here), stamped, and carries a
			// generation: fresh when there was none, preserved when the
			// folder already had one (§5.13a via §8.2).
			if tc.want == BuiltinUpdated || tc.want == BuiltinInstalled {
				requireSameTree(t, "plan", planDir)
				assert.FileExists(t, filepath.Join(planDir, ".omnipus-builtin"),
					"the installed/replaced folder carries the built-in stamp")
				gen := generationOf(t, planDir)
				assert.True(t, uuidShape.MatchString(gen),
					"the installed/replaced folder carries a UUID-format generation stamp")
				if tc.genKeepGen {
					assert.Equal(t, seededGeneration, gen,
						"the existing generation stamp is preserved across the content update (§5.13a)")
				}
			}

			if tc.want == BuiltinUnchanged {
				assert.True(t, dirMtime(t, planDir).Before(time.Now().Add(-time.Minute)),
					"untouched folder keeps its (back-dated) mtime — folder was not rewritten")
				assert.Equal(t, seededGeneration, generationOf(t, planDir),
					"the untouched folder keeps its generation")
			}
		})
	}
}

// TestSyncBuiltins_EditedUnstampedOverwritten_NoCustomCopy (S-7.2, r2.1
// founder F-28 greenfield): an unstamped edited built-in is overwritten, no
// <slug>-custom copy, grants unchanged, no banner.
// Traces to: skills-spec-r15.md §8.2 (r2.1 F-28 row), S-7.2, E-7.
func TestSyncBuiltins_EditedUnstampedOverwritten_NoCustomCopy(t *testing.T) {
	root := t.TempDir()
	planDir := writeSkillFixture(t, root, "plan") // edited content, unstamped

	grants := newSeededGrants("plan", "agent-a", "agent-b")
	report, err := SyncBuiltins(root, grants)
	require.NoError(t, err, "RED: SyncBuiltins not implemented on the baseline")

	assert.NotNil(t, findOutcome(report.Updated, "plan"), "the edited built-in is replaced (builtin_updated)")
	requireSameTree(t, "plan", planDir)
	assert.FileExists(t, filepath.Join(planDir, ".omnipus-builtin"), "stamp written on the replaced folder")
	assert.True(t, uuidShape.MatchString(generationOf(t, planDir)),
		"the replaced folder carries a fresh generation stamp (§8.2 r15)")
	_, err = os.Stat(filepath.Join(root, "plan-custom"))
	assert.True(t, errors.Is(err, os.ErrNotExist), "no <slug>-custom copy is kept (F-28)")
	assert.Equal(t, map[string][]string{
		"agent-a": {"plan", "keep-agent-a"},
		"agent-b": {"plan", "keep-agent-b"},
	}, grants.seeded, "a current built-in's complete grant lists are untouched")
	assert.Empty(t, grants.removed, "no grant removal for a current built-in")
}

// TestSyncBuiltins_SecondStartIsIdempotent (S-7.4): after a full sync, a
// second start changes nothing (folder mtime unchanged).
// Traces to: skills-spec-r15.md §8.2, S-7.4.
func TestSyncBuiltins_SecondStartIsIdempotent(t *testing.T) {
	root := t.TempDir()
	planDir := writeSkillFixture(t, root, "plan")
	builtinMarkerFixture(t, planDir)

	_, err := SyncBuiltins(root, nil)
	require.NoError(t, err, "RED: SyncBuiltins not implemented on the baseline")

	// Back-date and re-sync; the second start must not touch the equal folder.
	ageDir(t, planDir, time.Now().Add(-time.Hour))
	_, err = SyncBuiltins(root, nil)
	require.NoError(t, err)
	assert.True(t, dirMtime(t, planDir).Before(time.Now().Add(-time.Minute)),
		"second start leaves the now-equal folder untouched (mtime unchanged)")
}

// TestSyncBuiltins_UnstampedEqual_GetsStampOnly (S-7.5): content equals
// shipped → only the stamp is written, no copy.
// Traces to: skills-spec-r15.md §8.2 (stamp-only row, SD-1), S-7.5.
func TestSyncBuiltins_UnstampedEqual_GetsStampOnly(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(root, "plan")
	exportShipped(t, "plan", planDir) // shipped content, unstamped
	oldTime := time.Now().Add(-time.Hour)
	require.NoError(t, os.Chtimes(filepath.Join(planDir, "SKILL.md"), oldTime, oldTime))
	ageDir(t, planDir, time.Now().Add(-time.Hour))

	report, err := SyncBuiltins(root, nil)
	require.NoError(t, err)
	assert.Nil(t, findOutcome(report.Updated, "plan"), "equal content is not an update")
	assert.FileExists(t, filepath.Join(planDir, ".omnipus-builtin"), "the built-in stamp is written")
	assert.True(t, uuidShape.MatchString(generationOf(t, planDir)),
		"the unstamped equal folder is stamped with a fresh generation (§8.2 r15)")
	// The equality-shortcut writes stamps only: SKILL.md keeps its
	// back-dated mtime (the content was not rewritten; the FOLDER's mtime
	// changes because a file was added, so the oracle is the file's).
	requireSameTree(t, "plan", planDir)
	assert.True(t, fileMtime(t, filepath.Join(planDir, "SKILL.md")).Before(time.Now().Add(-time.Minute)),
		"only the stamps were written — SKILL.md was not rewritten")
}

// TestSyncBuiltins_RemovesStaleSeedStaging (S-7.10, F4): stale
// skills/.seed-<name>-* entries are removed first.
// Traces to: skills-spec-r15.md §8.2 (stale .seed-* removal, F4), S-7.10.
func TestSyncBuiltins_RemovesStaleSeedStaging(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, ".seed-plan-123")
	require.NoError(t, os.MkdirAll(stale, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stale, "SKILL.md"), []byte("stale\n"), 0o644))

	_, err := SyncBuiltins(root, nil)
	require.NoError(t, err)
	_, statErr := os.Stat(stale)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "stale skills/.seed-plan-123 removed at start")
}

// TestRetireBuiltins_StampedFolderDeleted_GrantRemoved (S-7.6): a stamped
// retired built-in folder is deleted and the name is removed from all 12
// granting agents.
// Traces to: skills-spec-r15.md §8.4 (stamped row), S-7.6, Skills NQ2 A.
func TestRetireBuiltins_StampedFolderDeleted_GrantRemoved(t *testing.T) {
	root := t.TempDir()
	handoffDir := writeSkillFixture(t, root, "handoff")
	builtinMarkerFixture(t, handoffDir)

	agents := make([]string, 12)
	for i := range agents {
		agents[i] = "agent-" + string(rune('a'+i))
	}
	grants := newSeededGrants("HaNdOfF", agents...)
	want := map[string][]string{"non-holder": {"unrelated-skill"}}
	grants.seeded["non-holder"] = []string{"unrelated-skill"}
	for _, agent := range agents {
		require.Equal(t, []string{"HaNdOfF", "keep-" + agent}, grants.seeded[agent], "fixture holder %s", agent)
		want[agent] = []string{"keep-" + agent}
	}
	report := &SyncReport{}

	require.NoError(t, RetireBuiltins(root, grants, report),
		"RED: RetireBuiltins not implemented on the baseline")

	assert.Contains(t, grants.removed, "handoff", "the contractual removal was requested by skill key")
	assert.Equal(t, want, grants.seeded, "all 12 holders lose only the retired grant; non-holders stay unchanged")
	_, statErr := os.Stat(handoffDir)
	assert.True(t, errors.Is(statErr, os.ErrNotExist), "the stamped retired folder is deleted")
	outcome := findRetirement(report.Retired, "handoff")
	require.NotNil(t, outcome, "the stamped-folder retirement is reported before dereferencing it")
	assert.True(t, outcome.FolderDeleted, "folder deleted reported (skill.builtin_retired)")
	assert.False(t, outcome.Kept, "the stamped folder was deleted, not kept")
	// RemoveGrant returns only an error: no agent-count claim can be derived
	// through this seam. The exact roster-state assertion above proves removal.
}

// TestRetireBuiltins_DanglingNameRemoved (S-7.7): no summarize folder; the
// dangling grant name is removed.
// Traces to: skills-spec-r15.md §8.4 (no-folder row), S-7.7.
func TestRetireBuiltins_DanglingNameRemoved(t *testing.T) {
	root := t.TempDir()
	grants := newSeededGrants("SuMmArIzE", "agent-a", "agent-b")
	require.Equal(t, map[string][]string{
		"agent-a": {"SuMmArIzE", "keep-agent-a"},
		"agent-b": {"SuMmArIzE", "keep-agent-b"},
	}, grants.seeded, "both agents start with the dangling grant")
	report := &SyncReport{}

	require.NoError(t, RetireBuiltins(root, grants, report),
		"RED: RetireBuiltins not implemented on the baseline")
	assert.Contains(t, grants.removed, "summarize", "absent folders still trigger removal")
	assert.Equal(t, map[string][]string{
		"agent-a": {"keep-agent-a"},
		"agent-b": {"keep-agent-b"},
	}, grants.seeded, "every dangling holder loses only the retired name")
	assert.NoDirExists(t, filepath.Join(root, "summarize"), "retirement never creates the absent folder")
	// §8.4 requires the grant side effect, not a report entry for an absent
	// folder. Inspect any emitted entry without assuming one or indexing it.
	for _, outcome := range report.Retired {
		if strings.EqualFold(outcome.Slug, "summarize") {
			assert.False(t, outcome.FolderDeleted, "no folder existed to delete")
			assert.False(t, outcome.Kept, "no unstamped folder existed to keep")
		}
	}
}

// TestRetireBuiltins_UnstampedFolderKept (S-7.8): an unstamped retired folder
// and its grants are untouched; skill.builtin_retired_kept once per start.
// Traces to: skills-spec-r15.md §8.4 (unstamped row), S-7.8.
func TestRetireBuiltins_UnstampedFolderKept(t *testing.T) {
	root := t.TempDir()
	summarizeDir := writeSkillFixture(t, root, "summarize") // unstamped
	grants := newSeededGrants("summarize", "agent-a")
	report := &SyncReport{}

	require.NoError(t, RetireBuiltins(root, grants, report),
		"RED: RetireBuiltins not implemented on the baseline")

	assert.FileExists(t, filepath.Join(summarizeDir, "SKILL.md"), "the unstamped folder is untouched")
	assert.Equal(t, map[string][]string{"agent-a": {"summarize", "keep-agent-a"}}, grants.seeded, "every grant is untouched")
	assert.NotContains(t, grants.removed, "summarize", "no removal was requested for the unstamped folder")
	outcome := findRetirement(report.Retired, "summarize")
	require.NotNil(t, outcome, "the kept-folder outcome exists before dereferencing it")
	assert.True(t, outcome.Kept, "reported kept (skill.builtin_retired_kept, once per start)")
	assert.False(t, outcome.FolderDeleted, "the kept folder was not reported deleted")
}

// TestRetireBuiltins_SkippedWhenABuiltinInstallFailed (S-7.9): a current
// built-in install failure skips retirement (the deleteOrphanedDefineDoneDir
// guard's property, kept).
// Traces to: skills-spec-r15.md §8.4 (ordering guard), S-7.9.
func TestRetireBuiltins_SkippedWhenABuiltinInstallFailed(t *testing.T) {
	root := t.TempDir()
	summarizeDir := writeSkillFixture(t, root, "summarize")
	builtinMarkerFixture(t, summarizeDir)

	// Fault: installing the first current built-in fails.
	previous := ShippedExportHook
	t.Cleanup(func() { ShippedExportHook = previous })
	ShippedExportHook = func(name, destDir string) error {
		if name == "add-connector" { // first current built-in
			return os.ErrPermission
		}
		return ExportEmbeddedPackage(name, destDir)
	}

	_, syncErr := SyncBuiltins(root, nil)
	require.Error(t, syncErr, "the failing built-in install surfaces the error")

	grants := newSeededGrants("summarize", "agent-a")
	report := &SyncReport{}

	// Retirement is SKIPPED this start (logged): no folder deleted, no grant
	// removed, and the report carries no retirement outcome.
	require.NoError(t, RetireBuiltins(root, grants, report),
		"RED: RetireBuiltins not implemented on the baseline")
	assert.FileExists(t, filepath.Join(summarizeDir, "SKILL.md"),
		"retirement skipped: the stamped retired folder is untouched")
	assert.Empty(t, report.Retired, "no retirement outcome recorded when a current built-in failed")
	assert.Equal(t, map[string][]string{"agent-a": {"summarize", "keep-agent-a"}}, grants.seeded, "no grant changes when retirement is skipped")
	assert.Empty(t, grants.removed, "skipped retirement never calls the editor")
}

// findOutcome returns the outcome for slug, or nil.
func findOutcome(outcomes []BuiltinOutcome, slug string) *BuiltinOutcome {
	for i := range outcomes {
		if outcomes[i].Slug == slug {
			return &outcomes[i]
		}
	}
	return nil
}

// findRetirement selects only the named fact; the one-method editor cannot
// certify how many other names had grants or must produce report entries.
func findRetirement(outcomes []RetiredOutcome, slug string) *RetiredOutcome {
	for i := range outcomes {
		if strings.EqualFold(outcomes[i].Slug, slug) {
			return &outcomes[i]
		}
	}
	return nil
}
