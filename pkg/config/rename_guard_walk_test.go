package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// walkRenameGuardSourceTree is the filesystem boundary shared by the real
// source guard and these deterministic traversal-race controls. Directory-entry
// metadata permits rejecting irrelevant files without a per-file Lstat; actual
// traversal/source-read errors still propagate through the visitor.
func walkRenameGuardSourceTree(base string, visit func(string, bool, error) error) error {
	return filepath.WalkDir(base, func(path string, entry fs.DirEntry, err error) error {
		return visit(path, entry != nil && entry.IsDir(), err)
	})
}

func TestRenameGuardWalkHandlesVanishingEntries(t *testing.T) {
	t.Run("vanished_non_source_does_not_hide_stable_source", func(t *testing.T) {
		root := t.TempDir()
		temporary := filepath.Join(root, ".tmp-atomic-write")
		stable := filepath.Join(root, "stable.go")
		for _, path := range []string{temporary, stable} {
			if err := os.WriteFile(path, []byte("package fixture\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var inspected []string
		err := walkRenameGuardSourceTree(root, func(path string, directory bool, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == root {
				// Walk has already enumerated this directory before its callback;
				// delete a listed entry before its per-entry Lstat, without sleeps.
				if err := os.Remove(temporary); err != nil {
					return err
				}
			}
			if directory || !strings.HasSuffix(path, ".go") {
				return nil
			}
			if _, err := os.ReadFile(path); err != nil {
				return err
			}
			inspected = append(inspected, filepath.Base(path))
			return nil
		})
		if err != nil {
			t.Errorf("irrelevant atomic temporary disappeared during traversal: %v", err)
		}
		if want := []string{"stable.go"}; !reflect.DeepEqual(inspected, want) {
			t.Errorf("inspected source files=%v, want exactly %v", inspected, want)
		}
	})

	t.Run("non_source_vanishes_after_directory_enumeration", func(t *testing.T) {
		root := t.TempDir()
		first := filepath.Join(root, "!first.go")
		temporary := filepath.Join(root, ".tmp-atomic-write")
		stable := filepath.Join(root, "stable.go")
		for _, path := range []string{first, temporary, stable} {
			if err := os.WriteFile(path, []byte("package fixture\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		var inspected []string
		err := walkRenameGuardSourceTree(root, func(path string, directory bool, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if path == first {
				// Both walkers have enumerated the directory. The lexically first
				// Go entry deletes the listed temporary before its own visit.
				if err := os.Remove(temporary); err != nil {
					return err
				}
			}
			if directory || !strings.HasSuffix(path, ".go") {
				return nil
			}
			if _, err := os.ReadFile(path); err != nil {
				return err
			}
			inspected = append(inspected, filepath.Base(path))
			return nil
		})
		if err != nil {
			t.Errorf("listed non-source temporary disappeared: %v", err)
		}
		if want := []string{"!first.go", "stable.go"}; !reflect.DeepEqual(inspected, want) {
			t.Errorf("inspected source files=%v, want exactly %v", inspected, want)
		}
	})

	t.Run("vanished_go_source_still_reports_an_error", func(t *testing.T) {
		root := t.TempDir()
		first := filepath.Join(root, "aaa.go")
		later := filepath.Join(root, "zzz.go")
		for _, path := range []string{first, later} {
			if err := os.WriteFile(path, []byte("package fixture\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		err := walkRenameGuardSourceTree(root, func(path string, directory bool, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if directory || !strings.HasSuffix(path, ".go") {
				return nil
			}
			if path == first {
				// Both walkers have enumerated the sibling before this callback.
				if err := os.Remove(later); err != nil {
					return err
				}
			}
			_, err := os.ReadFile(path)
			return err
		})
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("disappearing Go source error=%v, want os.ErrNotExist (no coverage loss hidden)", err)
		}
	})

	t.Run("visitor_errors_are_not_swallowed", func(t *testing.T) {
		root := t.TempDir()
		if err := os.WriteFile(filepath.Join(root, "source.go"), []byte("package fixture\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		want := errors.New("source inspection failed")
		err := walkRenameGuardSourceTree(root, func(_ string, directory bool, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if !directory {
				return want
			}
			return nil
		})
		if !errors.Is(err, want) {
			t.Errorf("visitor error=%v, want original %v", err, want)
		}
	})

	t.Run("missing_root_still_reports_an_error", func(t *testing.T) {
		root := filepath.Join(t.TempDir(), "missing")
		err := walkRenameGuardSourceTree(root, func(_ string, _ bool, walkErr error) error {
			return walkErr
		})
		if !errors.Is(err, os.ErrNotExist) {
			t.Errorf("missing root error=%v, want os.ErrNotExist", err)
		}
	})
}
