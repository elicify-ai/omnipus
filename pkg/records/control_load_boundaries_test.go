// Saved-view control-file loading boundaries (#1017).
// The oracle is the published regular-file and 256 KiB/128-file/4 MiB contract.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package records

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	controlBoundaryMarker = ".omnipus-vault"
	controlBoundarySchema = "schema_version: 1\ntype: invoice\nproperties:\n  client: { type: text }\n"
)

func controlBoundaryView(name string) string {
	return "name: " + name + "\ntype: invoice\nproperties: [client]\n"
}

// controlBoundarySized returns a valid view body that is exactly size bytes long.
func controlBoundarySized(t *testing.T, name string, size int) string {
	t.Helper()
	header := controlBoundaryView(name)
	body := header + "#" + strings.Repeat("x", size-len(header)-2) + "\n"
	require.Len(t, body, size, "fixture drift: exact byte length")
	return body
}

func controlBoundaryWrite(t *testing.T, path, body string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
}

// controlBoundaryVault returns a knowledge base root with one record type and an empty
// views folder.
func controlBoundaryVault(t *testing.T) string {
	t.Helper()
	vault := t.TempDir()
	controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "records", "invoice.yaml"), controlBoundarySchema)
	require.NoError(t, os.MkdirAll(filepath.Join(vault, controlBoundaryMarker, "views"), 0o700))
	return vault
}

// controlBoundaryFS records process-edge filesystem operations while delegating
// every operation to the real rooted filesystem.
type controlBoundaryFS struct {
	inner  ControlFS
	mu     sync.Mutex
	listed []string
	opened []string
}

func (s *controlBoundaryFS) Lstat(name string) (fs.FileInfo, error) { return s.inner.Lstat(name) }

func (s *controlBoundaryFS) ReadDir(name string) ([]fs.DirEntry, error) {
	s.mu.Lock()
	s.listed = append(s.listed, name)
	s.mu.Unlock()
	return s.inner.ReadDir(name)
}

func (s *controlBoundaryFS) OpenRegular(name string) (fs.File, error) {
	s.mu.Lock()
	s.opened = append(s.opened, name)
	s.mu.Unlock()
	return s.inner.OpenRegular(name)
}

func (s *controlBoundaryFS) openedUnder(dir string) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, n := range s.opened {
		if strings.HasPrefix(n, dir+"/") {
			out = append(out, n)
		}
	}
	return out
}

func controlBoundaryRecorder(t *testing.T, vault string) *controlBoundaryFS {
	t.Helper()
	root, err := os.OpenRoot(vault)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, root.Close()) })
	return &controlBoundaryFS{inner: RootControlFS{Root: root}}
}

func controlBoundaryLoad(t *testing.T, vault string) (CheckedControl, *controlBoundaryFS, error) {
	t.Helper()
	spy := controlBoundaryRecorder(t, vault)
	got, err := LoadControlChecked(context.Background(), spy, vault, ListingLoadLimits())
	return got, spy, err
}

// A YAML-named link to a regular file INSIDE the knowledge base is refused and is
// never opened, for schemas and for views alike (the published never-a-link rule).
func TestLoadControlChecked_InKnowledgeBaseLeafLinksAreRefusedAndNeverOpened(t *testing.T) {
	vault := controlBoundaryVault(t)
	controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "views", "ok.yaml"), controlBoundaryView("ok"))
	controlBoundaryWrite(t, filepath.Join(vault, "notes", "real-view.yaml"), controlBoundaryView("aliased"))
	controlBoundaryWrite(t, filepath.Join(vault, "notes", "real-schema.yaml"), "schema_version: 1\ntype: memo\nproperties:\n  title: { type: text }\n")
	require.NoError(t, os.Symlink("../../notes/real-view.yaml", filepath.Join(vault, controlBoundaryMarker, "views", "linked.yaml")))
	require.NoError(t, os.Symlink("../../notes/real-schema.yaml", filepath.Join(vault, controlBoundaryMarker, "records", "linked.yaml")))

	// Instrument check: both links resolve inside the root, so a refusal can only
	// come from the loader's own rule, not from an unresolvable link.
	root, err := os.OpenRoot(vault)
	require.NoError(t, err)
	for _, rel := range []string{controlBoundaryMarker + "/views/linked.yaml", controlBoundaryMarker + "/records/linked.yaml"} {
		f, openErr := root.Open(rel)
		require.NoError(t, openErr, "fixture check: %s must resolve inside the root", rel)
		require.NoError(t, f.Close())
	}
	require.NoError(t, root.Close())

	got, spy, err := controlBoundaryLoad(t, vault)
	require.NoError(t, err, "a refused file is a per-file rejection, not a load error")
	require.Equal(t, []string{"ok"}, got.Views.Names(), "the linked view must not be served")
	require.Equal(t, []string{"invoice"}, got.Schemas.Types(), "the linked record type must not be served")

	require.Len(t, got.ViewReport.Rejections, 1)
	require.Equal(t, RejectViewUnreadable, got.ViewReport.Rejections[0].Code)
	require.Equal(t, []string{filepath.Join(vault, controlBoundaryMarker, "views", "linked.yaml")}, got.ViewReport.Rejections[0].Paths)
	require.Len(t, got.SchemaReport.Rejections, 1)
	require.Equal(t, RejectUnreadable, got.SchemaReport.Rejections[0].Code)
	require.Equal(t, []string{filepath.Join(vault, controlBoundaryMarker, "records", "linked.yaml")}, got.SchemaReport.Rejections[0].Paths)

	require.NotContains(t, spy.opened, controlBoundaryMarker+"/views/linked.yaml", "the refused link must never be opened")
	require.NotContains(t, spy.opened, controlBoundaryMarker+"/records/linked.yaml", "the refused link must never be opened")
}

// A YAML-named link to a file OUTSIDE the knowledge base is refused, never opened,
// and its target is not named in any rejection text (the published containment rule).
func TestLoadControlChecked_OutwardLeafLinksAreRefusedNeverOpenedAndNeverNamed(t *testing.T) {
	outside := t.TempDir()
	controlBoundaryWrite(t, filepath.Join(outside, "secret-view.yaml"), controlBoundaryView("leak"))
	controlBoundaryWrite(t, filepath.Join(outside, "secret-schema.yaml"), "schema_version: 1\ntype: leaktype\nproperties:\n  a: { type: text }\n")
	vault := controlBoundaryVault(t)
	controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "views", "ok.yaml"), controlBoundaryView("ok"))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret-view.yaml"), filepath.Join(vault, controlBoundaryMarker, "views", "escape.yaml")))
	require.NoError(t, os.Symlink(filepath.Join(outside, "secret-schema.yaml"), filepath.Join(vault, controlBoundaryMarker, "records", "escape.yaml")))

	got, spy, err := controlBoundaryLoad(t, vault)
	require.NoError(t, err)
	require.Equal(t, []string{"ok"}, got.Views.Names())
	require.Equal(t, []string{"invoice"}, got.Schemas.Types())
	require.Len(t, got.ViewReport.Rejections, 1)
	require.Equal(t, RejectViewUnreadable, got.ViewReport.Rejections[0].Code)
	require.Len(t, got.SchemaReport.Rejections, 1)
	require.Equal(t, RejectUnreadable, got.SchemaReport.Rejections[0].Code)
	require.NotContains(t, spy.opened, controlBoundaryMarker+"/views/escape.yaml", "no forbidden loader read")
	require.NotContains(t, spy.opened, controlBoundaryMarker+"/records/escape.yaml", "no forbidden loader read")
	for _, rej := range got.ViewReport.Rejections {
		require.NotContains(t, rej.Reason, outside, "a rejection must not name the link target")
	}
	for _, rej := range got.SchemaReport.Rejections {
		require.NotContains(t, rej.Reason, outside, "a rejection must not name the link target")
	}
}

// A control folder (marker, record types, views) that is a link is refused before
// anything in it is listed, whether the link points inside or outside the knowledge
// base (the control-folder loading rule).
func TestLoadControlChecked_LinkedControlFoldersAreRefusedBeforeAnythingInThemIsListed(t *testing.T) {
	cases := []struct {
		name      string
		build     func(t *testing.T, vault string)
		neverList string // a folder the loader must not list; "" means it must list nothing at all
	}{
		{
			name: "views folder is a link to a real folder inside the knowledge base",
			build: func(t *testing.T, vault string) {
				controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "records", "invoice.yaml"), controlBoundarySchema)
				controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "real-views", "ok.yaml"), controlBoundaryView("ok"))
				require.NoError(t, os.Symlink("real-views", filepath.Join(vault, controlBoundaryMarker, "views")))
			},
			neverList: controlBoundaryMarker + "/views",
		},
		{
			name: "records folder is a link to a real folder inside the knowledge base",
			build: func(t *testing.T, vault string) {
				controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "real-records", "invoice.yaml"), controlBoundarySchema)
				require.NoError(t, os.MkdirAll(filepath.Join(vault, controlBoundaryMarker, "views"), 0o700))
				require.NoError(t, os.Symlink("real-records", filepath.Join(vault, controlBoundaryMarker, "records")))
			},
			neverList: controlBoundaryMarker + "/records",
		},
		{
			name: "marker folder is a link to a real folder inside the knowledge base",
			build: func(t *testing.T, vault string) {
				controlBoundaryWrite(t, filepath.Join(vault, "real-vault", "records", "invoice.yaml"), controlBoundarySchema)
				controlBoundaryWrite(t, filepath.Join(vault, "real-vault", "views", "ok.yaml"), controlBoundaryView("ok"))
				require.NoError(t, os.Symlink("real-vault", filepath.Join(vault, controlBoundaryMarker)))
			},
			neverList: "",
		},
		{
			name: "views folder is a link to a folder outside the knowledge base",
			build: func(t *testing.T, vault string) {
				outside := t.TempDir()
				controlBoundaryWrite(t, filepath.Join(outside, "leak.yaml"), controlBoundaryView("leak"))
				controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "records", "invoice.yaml"), controlBoundarySchema)
				require.NoError(t, os.Symlink(outside, filepath.Join(vault, controlBoundaryMarker, "views")))
			},
			neverList: controlBoundaryMarker + "/views",
		},
		{
			name: "records folder is a link to a folder outside the knowledge base",
			build: func(t *testing.T, vault string) {
				outside := t.TempDir()
				controlBoundaryWrite(t, filepath.Join(outside, "leak.yaml"), "schema_version: 1\ntype: leaktype\nproperties:\n  a: { type: text }\n")
				require.NoError(t, os.MkdirAll(filepath.Join(vault, controlBoundaryMarker, "views"), 0o700))
				require.NoError(t, os.Symlink(outside, filepath.Join(vault, controlBoundaryMarker, "records")))
			},
			neverList: controlBoundaryMarker + "/records",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := t.TempDir()
			tc.build(t, vault)
			got, spy, err := controlBoundaryLoad(t, vault)
			require.Error(t, err, "a linked control folder is a load error, never read through the link")
			require.Nil(t, got.Views, "nothing is returned when the load is refused")
			if tc.neverList == "" {
				require.Empty(t, spy.listed, "nothing may be listed under a linked marker folder")
				require.Empty(t, spy.opened, "no file is opened under a linked marker folder")
			} else {
				require.NotContains(t, spy.listed, tc.neverList, "the linked folder must not be listed")
				require.Empty(t, spy.openedUnder(tc.neverList), "no file is opened through a linked control folder")
			}
		})
	}
}

// 128 candidate files are read per folder; the 129th is a per-file rejection and is
// never opened (published limit).
func TestLoadControlChecked_CandidateLimitIsExactly128PerFolder(t *testing.T) {
	vault := controlBoundaryVault(t)
	for i := 0; i <= 128; i++ {
		name := fmt.Sprintf("v%03d", i)
		controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "views", name+".yaml"), controlBoundaryView(name))
	}
	got, spy, err := controlBoundaryLoad(t, vault)
	require.NoError(t, err)
	require.Len(t, got.Views.Names(), 128, "128 candidate files are read per folder")
	require.Len(t, got.ViewReport.Rejections, 1)
	require.Equal(t, RejectViewUnreadable, got.ViewReport.Rejections[0].Code)
	require.Equal(t, []string{filepath.Join(vault, controlBoundaryMarker, "views", "v128.yaml")}, got.ViewReport.Rejections[0].Paths)
	require.Len(t, spy.openedUnder(controlBoundaryMarker+"/views"), 128, "the 129th candidate is never opened")
}

// Entries that are not candidates (other extensions, dot files, folders) do not use up
// the 128-file budget.
func TestLoadControlChecked_NonCandidateNamesDoNotUseTheCandidateBudget(t *testing.T) {
	vault := controlBoundaryVault(t)
	for i := 0; i < 128; i++ {
		name := fmt.Sprintf("v%03d", i)
		controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "views", name+".yaml"), controlBoundaryView(name))
	}
	controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "views", "README.md"), "notes\n")
	controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "views", ".hidden.yaml"), controlBoundaryView("hidden"))
	controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "views", "a-notes.txt"), "x\n")
	require.NoError(t, os.MkdirAll(filepath.Join(vault, controlBoundaryMarker, "views", "a-folder.yaml"), 0o700))
	got, _, err := controlBoundaryLoad(t, vault)
	require.NoError(t, err)
	require.Len(t, got.Views.Names(), 128)
	require.Empty(t, got.ViewReport.Rejections)
}

// 4 MiB per load, schemas and views together: the schema plus 15 files of exactly
// 256 KiB fit; the 16th would take the total over 4 MiB (published limit).
func TestLoadControlChecked_FourMiBBudgetCoversSchemasAndViewsTogether(t *testing.T) {
	vault := controlBoundaryVault(t)
	const fileBytes = 256 << 10
	for i := 1; i <= 17; i++ {
		name := fmt.Sprintf("a%02d", i)
		controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "views", name+".yaml"), controlBoundarySized(t, name, fileBytes))
	}
	got, _, err := controlBoundaryLoad(t, vault)
	require.NoError(t, err)
	require.Equal(t, 15, got.Views.Len(), "the schema and 15 full-size views fit in 4 MiB; a 16th does not")
	require.Len(t, got.ViewReport.Rejections, 2)
	for _, rej := range got.ViewReport.Rejections {
		require.Equal(t, RejectViewUnreadable, rej.Code)
	}
}

// The 256 KiB boundary holds for record-type files too, not only views.
func TestLoadControlChecked_SchemaFileBoundaryIs256KiB(t *testing.T) {
	vault := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(vault, controlBoundaryMarker, "records"), 0o700))
	for _, tc := range []struct {
		typ  string
		size int
	}{{"fit", 256 << 10}, {"over", 256<<10 + 1}} {
		header := "schema_version: 1\ntype: " + tc.typ + "\nproperties:\n  client: { type: text }\n"
		body := header + "#" + strings.Repeat("x", tc.size-len(header)-2) + "\n"
		require.Len(t, body, tc.size, "fixture drift: exact byte length")
		controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "records", tc.typ+".yaml"), body)
	}
	got, _, err := controlBoundaryLoad(t, vault)
	require.NoError(t, err)
	require.Equal(t, []string{"fit"}, got.Schemas.Types())
	require.Len(t, got.SchemaReport.Rejections, 1)
	require.Equal(t, RejectUnreadable, got.SchemaReport.Rejections[0].Code)
	require.Equal(t, []string{filepath.Join(vault, controlBoundaryMarker, "records", "over.yaml")}, got.SchemaReport.Rejections[0].Paths)
}

// A control folder that does not exist is an empty set, not an error, and nothing is listed.
func TestLoadControlChecked_MissingControlFoldersAreEmptySets(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T, vault string)
	}{
		{"no marker folder at all", func(t *testing.T, vault string) {}},
		{"marker folder only", func(t *testing.T, vault string) {
			require.NoError(t, os.MkdirAll(filepath.Join(vault, controlBoundaryMarker), 0o700))
		}},
		{"records folder only", func(t *testing.T, vault string) {
			controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "records", "invoice.yaml"), controlBoundarySchema)
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			vault := t.TempDir()
			tc.build(t, vault)
			got, _, err := controlBoundaryLoad(t, vault)
			require.NoError(t, err)
			require.Zero(t, got.Views.Len())
			require.Empty(t, got.ViewReport.Rejections)
		})
	}
}

// A cancelled context stops the load before anything is opened.
func TestLoadControlChecked_CancelledContextStopsTheLoad(t *testing.T) {
	vault := controlBoundaryVault(t)
	controlBoundaryWrite(t, filepath.Join(vault, controlBoundaryMarker, "views", "ok.yaml"), controlBoundaryView("ok"))
	spy := controlBoundaryRecorder(t, vault)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := LoadControlChecked(ctx, spy, vault, ListingLoadLimits())
	require.ErrorIs(t, err, context.Canceled)
	require.Empty(t, spy.opened)
}
