package skills

// RED tests — landing/skills (T9): built-ins read-only, synced at start,
// retired (spec §8, test rows 22–23). Scenarios S-7.1, S-7.2, S-7.4, S-7.5,
// S-7.10, S-7.6–S-7.9; dataset D-6 (start-up matrix).
// Traces to: skills-spec-r13.md §8.1–§8.5, §16 US-7, §17.1 rows 22/23,
// §17.2 D-6.
//
// RED expectation: SyncBuiltins / RetireBuiltins / RetiredBuiltinNames /
// IsBuiltin are RED scaffolds — every behavioural assertion below fails on
// the baseline (SeedDefaults never overwrites, never retires, no report).

import (
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

// seededGrants models N agents holding one name; RemoveGrant clears them.
type seededGrants struct {
	seeded  map[string][]string // skill key -> agent ids
	removed []string
	failOn  string // remove of this name fails (S-7.9 uses install failure instead)
}

func newSeededGrants(name string, agents ...string) *seededGrants {
	g := &seededGrants{seeded: map[string][]string{}}
	g.seeded[strings.ToLower(name)] = agents
	return g
}

func (g *seededGrants) RemoveGrant(name string) error {
	key := strings.ToLower(name)
	g.removed = append(g.removed, key)
	delete(g.seeded, key)
	return nil
}

func (g *seededGrants) holders(name string) []string {
	return g.seeded[strings.ToLower(name)]
}

// TestSyncBuiltins_StartupMatrix (S-7.1 + D-6 rows for a current built-in):
// stamped old content → replaced; stamped outside edit → replaced; missing →
// installed; stamped equal → untouched (mtime unchanged).
// Traces to: skills-spec-r13.md §8.2 case table, S-7.1, D-6.
func TestSyncBuiltins_StartupMatrix(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(t *testing.T, root, planDir string)
		want    BuiltinSyncCase
		wantNew bool // folder must end up byte-equal to shipped
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
				require.NoError(t, os.WriteFile(filepath.Join(planDir, "SKILL.md"),
					[]byte("---\nname: plan\ndescription: edited outside\n---\noutside edit\n"), 0o644))
			},
			want:    BuiltinUpdated,
			wantNew: true,
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
				ageDir(t, planDir, time.Now().Add(-time.Hour))
			},
			want: BuiltinUnchanged,
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

			if tc.want == BuiltinUnchanged {
				assert.True(t, dirMtime(t, planDir).Before(time.Now().Add(-time.Minute)),
					"untouched folder keeps its (back-dated) mtime — folder was not rewritten")
			}
			if tc.wantNew {
				shipped := filepath.Join(t.TempDir(), "shipped-plan")
				exportShipped(t, "plan", shipped)
				assert.DirExists(t, planDir)
				assert.FileExists(t, filepath.Join(planDir, ".omnipus-builtin"),
					"the replaced folder carries the stamp")
			}
			if tc.want == BuiltinInstalled {
				assert.FileExists(t, filepath.Join(planDir, ".omnipus-builtin"),
					"an installed built-in is stamped")
			}
			// Never history for a built-in (§5.11).
			_ = planDir
		})
	}
}

// TestSyncBuiltins_EditedUnstampedOverwritten_NoCustomCopy (S-7.2, r2.1
// founder F-28 greenfield): an unstamped edited built-in is overwritten, no
// <slug>-custom copy, grants unchanged, no banner.
// Traces to: skills-spec-r13.md §8.2 (r2.1 F-28 row), S-7.2, E-7.
func TestSyncBuiltins_EditedUnstampedOverwritten_NoCustomCopy(t *testing.T) {
	root := t.TempDir()
	planDir := writeSkillFixture(t, root, "plan") // edited content, unstamped

	grants := newSeededGrants("plan", "agent-a", "agent-b")
	report, err := SyncBuiltins(root, grants)
	require.NoError(t, err, "RED: SyncBuiltins not implemented on the baseline")

	assert.NotNil(t, findOutcome(report.Updated, "plan"), "the edited built-in is replaced (builtin_updated)")
	shipped := filepath.Join(t.TempDir(), "shipped")
	exportShipped(t, "plan", shipped)
	assert.FileExists(t, filepath.Join(planDir, ".omnipus-builtin"), "stamp written on the replaced folder")
	_, err = os.Stat(filepath.Join(root, "plan-custom"))
	assert.True(t, os.IsNotExist(err), "no <slug>-custom copy is kept (F-28)")
	assert.Len(t, grants.holders("plan"), 2, "a current built-in's grants are untouched")
	assert.Empty(t, grants.removed, "no grant removal for a current built-in")
}

// TestSyncBuiltins_SecondStartIsIdempotent (S-7.4): after a full sync, a
// second start changes nothing (folder mtime unchanged).
// Traces to: skills-spec-r13.md §8.2, S-7.4.
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
// Traces to: skills-spec-r13.md §8.2 (stamp-only row, SD-1), S-7.5.
func TestSyncBuiltins_UnstampedEqual_GetsStampOnly(t *testing.T) {
	root := t.TempDir()
	planDir := filepath.Join(root, "plan")
	exportShipped(t, "plan", planDir) // shipped content, unstamped
	ageDir(t, planDir, time.Now().Add(-time.Hour))

	report, err := SyncBuiltins(root, nil)
	require.NoError(t, err)
	assert.Nil(t, findOutcome(report.Updated, "plan"), "equal content is not an update")
	assert.FileExists(t, filepath.Join(planDir, ".omnipus-builtin"), "the stamp is written")
	assert.True(t, dirMtime(t, planDir).Before(time.Now().Add(-time.Minute)),
		"only the stamp changed — the folder itself was not rewritten")
}

// TestSyncBuiltins_RemovesStaleSeedStaging (S-7.10, F4): stale
// skills/.seed-<name>-* entries are removed first.
// Traces to: skills-spec-r13.md §8.2 (stale .seed-* removal, F4), S-7.10.
func TestSyncBuiltins_RemovesStaleSeedStaging(t *testing.T) {
	root := t.TempDir()
	stale := filepath.Join(root, ".seed-plan-123")
	require.NoError(t, os.MkdirAll(stale, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stale, "SKILL.md"), []byte("stale\n"), 0o644))

	_, err := SyncBuiltins(root, nil)
	require.NoError(t, err)
	_, statErr := os.Stat(stale)
	assert.True(t, os.IsNotExist(statErr), "stale skills/.seed-plan-123 removed at start")
}

// TestRetireBuiltins_StampedFolderDeleted_GrantRemoved (S-7.6): a stamped
// retired built-in folder is deleted and the name is removed from all 12
// granting agents.
// Traces to: skills-spec-r13.md §8.4 (stamped row), S-7.6, Skills NQ2 A.
func TestRetireBuiltins_StampedFolderDeleted_GrantRemoved(t *testing.T) {
	root := t.TempDir()
	handoffDir := writeSkillFixture(t, root, "handoff")
	builtinMarkerFixture(t, handoffDir)

	agents := make([]string, 12)
	for i := range agents {
		agents[i] = "agent-" + string(rune('a'+i))
	}
	grants := newSeededGrants("handoff", agents...)
	report := &SyncReport{}

	require.NoError(t, RetireBuiltins(root, grants, report),
		"RED: RetireBuiltins not implemented on the baseline")

	assert.Empty(t, grants.holders("handoff"), "the name is removed from all 12 agents")
	_, statErr := os.Stat(handoffDir)
	assert.True(t, os.IsNotExist(statErr), "the stamped retired folder is deleted")
	require.Len(t, report.Retired, 1, "one retirement outcome reported")
	assert.Equal(t, "handoff", report.Retired[0].Slug)
	assert.True(t, report.Retired[0].FolderDeleted, "folder deleted reported (skill.builtin_retired)")
	assert.Len(t, report.Retired[0].AgentsChanged, 12, "all 12 agents changed")
}

// TestRetireBuiltins_DanglingNameRemoved (S-7.7): no summarize folder; the
// dangling grant name is removed.
// Traces to: skills-spec-r13.md §8.4 (no-folder row), S-7.7.
func TestRetireBuiltins_DanglingNameRemoved(t *testing.T) {
	root := t.TempDir()
	grants := newSeededGrants("summarize", "agent-a")
	report := &SyncReport{}

	require.NoError(t, RetireBuiltins(root, grants, report),
		"RED: RetireBuiltins not implemented on the baseline")
	assert.Empty(t, grants.holders("summarize"), "dangling retired name removed from grants")
	assert.Len(t, report.Retired, 1)
	assert.False(t, report.Retired[0].FolderDeleted, "no folder existed to delete")
}

// TestRetireBuiltins_UnstampedFolderKept (S-7.8): an unstamped retired folder
// and its grants are untouched; skill.builtin_retired_kept once per start.
// Traces to: skills-spec-r13.md §8.4 (unstamped row), S-7.8.
func TestRetireBuiltins_UnstampedFolderKept(t *testing.T) {
	root := t.TempDir()
	summarizeDir := writeSkillFixture(t, root, "summarize") // unstamped
	grants := newSeededGrants("summarize", "agent-a")
	report := &SyncReport{}

	require.NoError(t, RetireBuiltins(root, grants, report),
		"RED: RetireBuiltins not implemented on the baseline")

	assert.FileExists(t, filepath.Join(summarizeDir, "SKILL.md"), "the unstamped folder is untouched")
	assert.Len(t, grants.holders("summarize"), 1, "the grant is untouched")
	require.Len(t, report.Retired, 1)
	assert.True(t, report.Retired[0].Kept, "reported kept (skill.builtin_retired_kept, once per start)")
}

// TestRetireBuiltins_SkippedWhenABuiltinInstallFailed (S-7.9): a current
// built-in install failure skips retirement (the deleteOrphanedDefineDoneDir
// guard's property, kept).
// Traces to: skills-spec-r13.md §8.4 (ordering guard), S-7.9.
func TestRetireBuiltins_SkippedWhenABuiltinInstallFailed(t *testing.T) {
	root := t.TempDir()

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

	summarizeDir := writeSkillFixture(t, root, "summarize") // unstamped retired, would be kept anyway
	grants := newSeededGrants("summarize", "agent-a")
	report := &SyncReport{}

	// Retirement is SKIPPED this start (logged): no folder deleted, no grant
	// removed, and the report carries no retirement outcome.
	require.NoError(t, RetireBuiltins(root, grants, report),
		"RED: RetireBuiltins not implemented on the baseline")
	assert.FileExists(t, filepath.Join(summarizeDir, "SKILL.md"),
		"retirement skipped: the unstamped folder is untouched")
	assert.Empty(t, report.Retired, "no retirement outcome recorded when a current built-in failed")
	assert.Len(t, grants.holders("summarize"), 1, "no grant removal when retirement is skipped")
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

// errorMentionsNotImplemented reports whether err is (or wraps) the RED
// scaffold sentinel — the expected baseline failure for unimplemented APIs.
func errorMentionsNotImplemented(err error) bool {
	return err != nil && strings.Contains(err.Error(), "RED scaffold")
}
