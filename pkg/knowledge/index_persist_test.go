// index_persist_test.go: tests for persist and load the index on disk — open-or-rebuild, the format and mapping guards, and index-directory permissions.

package knowledge

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/blevesearch/bleve/v2"
	bleveMapping "github.com/blevesearch/bleve/v2/mapping"
	"github.com/stretchr/testify/require"
)

// --- moved from index.go tests 2026-09-15 ---

func TestEnforceEntryPermissions_VanishedSegmentIsNotAnError(t *testing.T) {
	// 1. The walk itself could not stat an entry it had enumerated.
	require.NoError(t,
		enforceEntryPermissions("000000000005.zap", nil,
			&fs.PathError{Op: "lstat", Path: "000000000005.zap", Err: fs.ErrNotExist}),
		"a segment the merger deleted must not fail the whole Sync")

	// 2. DirEntry.Info() lstats lazily and finds the file gone.
	require.NoError(t,
		enforceEntryPermissions("000000000005.zap", vanishedEntry{name: "000000000005.zap"}, nil),
		"a lazily-lstat'ed entry that vanished must not fail the whole Sync")

	// 3. Gone between Info() and Chmod: a real file, stat'ed, then deleted.
	dir := t.TempDir()
	path := filepath.Join(dir, "000000000006.zap")
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o644)) // wrong perms on purpose
	fi, err := os.Lstat(path)
	require.NoError(t, err)
	require.NoError(t, os.Remove(path))
	require.NoError(t, enforceEntryPermissions(path, realEntry{fi: fi}, nil),
		"a file deleted between Info and Chmod must not fail the whole Sync")
}

// The other half: tolerating ENOENT must not have turned the permission
// enforcement itself off. Without this, deleting the chmod entirely would
// still leave the test above green.
func TestEnforceEntryPermissions_StillEnforcesOnFilesThatExist(t *testing.T) {
	dir := t.TempDir()

	// bleve's index_meta.json is created 0666 — the exact case FR-032 exists
	// for, since the index holds the full text of every note.
	path := filepath.Join(dir, "index_meta.json")
	require.NoError(t, os.WriteFile(path, []byte("{}"), 0o666))
	fi, err := os.Lstat(path)
	require.NoError(t, err)
	require.NoError(t, enforceEntryPermissions(path, realEntry{fi: fi}, nil))

	after, err := os.Lstat(path)
	require.NoError(t, err)
	require.Equal(t, indexFileMode, after.Mode().Perm(),
		"a file that still exists must still be chmod'ed to 0600")

	sub := filepath.Join(dir, "store")
	require.NoError(t, os.Mkdir(sub, 0o777))
	dfi, err := os.Lstat(sub)
	require.NoError(t, err)
	require.NoError(t, enforceEntryPermissions(sub, realEntry{fi: dfi}, nil))

	dafter, err := os.Lstat(sub)
	require.NoError(t, err)
	require.Equal(t, indexDirMode, dafter.Mode().Perm(),
		"a directory that still exists must still be chmod'ed to 0700")
}

// A genuine error is still a genuine error: only ErrNotExist is tolerated.
func TestEnforceEntryPermissions_OtherErrorsStillPropagate(t *testing.T) {
	boom := &fs.PathError{Op: "lstat", Path: "x", Err: os.ErrPermission}
	require.Error(t, enforceEntryPermissions("x", nil, boom),
		"a permission error is not a vanished file and must still fail the walk")
}

// TestMappingDrift_ReportsEveryKindOfDivergence checks G2 against a persisted
// mapping that has genuinely round-tripped through a real index — not against
// an in-memory struct, because the round trip is where a comparison of
// omitempty booleans would quietly stop working.
func TestMappingDrift_ReportsEveryKindOfDivergence(t *testing.T) {
	for _, tc := range w0MappingCases() {
		t.Run(tc.name, func(t *testing.T) {
			m := buildIndexMapping()
			tc.mutate(m)

			path := filepath.Join(t.TempDir(), "bleve")
			w0BuildIndexWithMapping(t, path, m)
			idx, err := bleve.OpenUsing(path, map[string]any{"bolt_timeout": boltOpenTimeout})
			if err != nil {
				t.Fatalf("OpenUsing: %v", err)
			}
			defer func() {
				if cErr := idx.Close(); cErr != nil {
					t.Errorf("Close: %v", cErr)
				}
			}()

			got := mappingDrift(idx.Mapping())
			if got == "" {
				t.Fatalf("mappingDrift found nothing wrong with a mapping that differs: %s", tc.name)
			}
			if !strings.Contains(got, tc.wantSub) {
				t.Errorf("mappingDrift = %q, want it to contain %q", got, tc.wantSub)
			}
		})
	}
}

// TestMappingDrift_SilentOnTheShippingMapping is G2's control: an index built
// by this code, reopened by this code, must produce NO drift. Without it G2
// could be a rebuild-always guard and every other G2 test would still pass.
func TestMappingDrift_SilentOnTheShippingMapping(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bleve")
	w0BuildIndexWithMapping(t, path, buildIndexMapping())
	idx, err := bleve.OpenUsing(path, map[string]any{"bolt_timeout": boltOpenTimeout})
	if err != nil {
		t.Fatalf("OpenUsing: %v", err)
	}
	defer func() {
		if cErr := idx.Close(); cErr != nil {
			t.Errorf("Close: %v", cErr)
		}
	}()
	if got := mappingDrift(idx.Mapping()); got != "" {
		t.Errorf("mappingDrift on an unchanged mapping = %q, want \"\" — a guard that always fires is a guard "+
			"that rebuilds the index on every restart", got)
	}
}

// TestMappingDrift_NameOnlyComparisonWouldMissTheAnalyzer is the spike's §4.4
// mutation, kept as a test rather than a claim.
//
// It asserts two things at once: that G2 catches an analyzer change, and that
// the WEAKER guard someone might write instead — comparing field names — could
// not have. The second assertion is what stops G2 from being quietly reduced to
// a name check later: weaken it that way and this test fails, because the two
// mappings' field-name sets are proved identical here.
func TestMappingDrift_NameOnlyComparisonWouldMissTheAnalyzer(t *testing.T) {
	drifted := buildIndexMapping()
	fm := bleve.NewTextFieldMapping()
	fm.Analyzer = "keyword"
	fm.Store, fm.IncludeTermVectors, fm.IncludeInAll, fm.DocValues = false, false, false, false
	drifted.DefaultMapping.Properties[fieldName] = bleveMapping.NewDocumentMapping()
	drifted.DefaultMapping.AddFieldMappingsAt(fieldName, fm)

	shipping := buildIndexMapping()
	names := func(m *bleveMapping.IndexMappingImpl) string {
		out := make([]string, 0, len(m.DefaultMapping.Properties))
		for n := range m.DefaultMapping.Properties {
			out = append(out, n)
		}
		sort.Strings(out)
		return strings.Join(out, ",")
	}
	if names(drifted) != names(shipping) {
		t.Fatalf("the mutation changed the field-name set (%s vs %s); it no longer demonstrates what a name-only "+
			"guard misses", names(drifted), names(shipping))
	}

	path := filepath.Join(t.TempDir(), "bleve")
	w0BuildIndexWithMapping(t, path, drifted)
	idx, err := bleve.OpenUsing(path, map[string]any{"bolt_timeout": boltOpenTimeout})
	if err != nil {
		t.Fatalf("OpenUsing: %v", err)
	}
	defer func() {
		if cErr := idx.Close(); cErr != nil {
			t.Errorf("Close: %v", cErr)
		}
	}()
	got := mappingDrift(idx.Mapping())
	if !strings.Contains(got, `field "name" uses analyzer "keyword"`) {
		t.Errorf("mappingDrift = %q, want it to name the analyzer difference a name-only comparison cannot see", got)
	}
}
