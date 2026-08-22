// Tests for the knowledge base freshness manifest (ADR-067 FR-033, FR-032).
//
// Every expected value here comes from the spec, not from the implementation:
// FR-033 names size, modification time and content hash as the three change
// criteria; FR-032 names 0700 directories and 0600 files.
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package knowledge

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// TestManifest_RoundTripsWhatTheIndexNeedsToRebuild asserts that everything the
// incremental path depends on survives a save/load cycle. The segment count is
// the one most easily forgotten and the most damaging to lose: deleting a file
// from the index means deleting every document it produced (FR-034a), and only
// the manifest remembers how many there were.
func TestManifest_RoundTripsWhatTheIndexNeedsToRebuild(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ManifestFileName)
	root := "/collections/research"

	m := NewManifest(root)
	m.Put(ManifestEntry{
		Path: "notes/big.md", Kind: ScanKindNote,
		Size: 20 << 20, ModTimeNanos: 1_700_000_000_123_456_789,
		Hash: "abc123", Segments: 3,
	})
	m.Put(ManifestEntry{
		Path: "img/diagram-v3.png", Kind: ScanKindAttachment,
		Size: 4096, ModTimeNanos: 42, Hash: "", Segments: 1,
	})
	if err := m.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	back, err := LoadManifest(path, root)
	if err != nil {
		t.Fatalf("LoadManifest: %v", err)
	}
	if back.Len() != 2 {
		t.Fatalf("Len = %d, want 2", back.Len())
	}
	note, ok := back.Get("notes/big.md")
	if !ok {
		t.Fatal("notes/big.md missing from reloaded manifest")
	}
	if note.Segments != 3 {
		t.Errorf("Segments = %d, want 3 — without it the index cannot delete a segmented note's documents", note.Segments)
	}
	if note.Hash != "abc123" || note.Size != 20<<20 || note.ModTimeNanos != 1_700_000_000_123_456_789 {
		t.Errorf("note record = %+v, want the saved size/mtime/hash", note)
	}
	att, ok := back.Get("img/diagram-v3.png")
	if !ok {
		t.Fatal("img/diagram-v3.png missing from reloaded manifest")
	}
	if att.Hash != "" {
		t.Errorf("attachment Hash = %q, want empty — hashing an attachment means opening it, which FR-039a forbids", att.Hash)
	}
}

// TestManifest_AbsentIsAFirstRunNotAnError: a missing manifest is the ordinary
// first-open case. Returning an error there would make every fresh collection
// look broken.
func TestManifest_AbsentIsAFirstRunNotAnError(t *testing.T) {
	m, err := LoadManifest(filepath.Join(t.TempDir(), ManifestFileName), "/root")
	if err != nil {
		t.Fatalf("LoadManifest on a missing file: err = %v, want nil", err)
	}
	if m == nil || m.Len() != 0 {
		t.Fatalf("want an empty manifest, got %+v", m)
	}
}

// TestManifest_UnusableRecordIsDiscardedAndReported covers the three ways a
// manifest can be present but must not be trusted. Each must yield an EMPTY
// manifest (so the caller rebuilds — slower, never wrong) AND a non-nil error
// (so the reason is on the record rather than swallowed).
//
// The foreign-root case is the one that matters most: applying one collection's
// record to another would mark every file "unchanged" and produce an index of a
// collection that was never read.
func TestManifest_UnusableRecordIsDiscardedAndReported(t *testing.T) {
	const root = "/collections/a"

	cases := []struct {
		name  string
		write func(t *testing.T, path string)
		root  string
	}{
		{
			name: "corrupt json",
			write: func(t *testing.T, path string) {
				t.Helper()
				if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			root: root,
		},
		{
			name: "unrecognised version",
			write: func(t *testing.T, path string) {
				t.Helper()
				raw := `{"version":99,"root":"` + root + `","entries":{"a.md":{"path":"a.md","kind":"note","size":1,"mtime_ns":1,"segments":1}}}`
				if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			root: root,
		},
		{
			name: "recorded against a different collection",
			write: func(t *testing.T, path string) {
				t.Helper()
				m := NewManifest("/collections/somewhere-else")
				m.Put(ManifestEntry{Path: "a.md", Kind: ScanKindNote, Size: 1, ModTimeNanos: 1, Segments: 1})
				if err := m.Save(path); err != nil {
					t.Fatal(err)
				}
			},
			root: root,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ManifestFileName)
			tc.write(t, path)

			m, err := LoadManifest(path, tc.root)
			if err == nil {
				t.Error("err = nil, want a reported reason — a silently discarded manifest is an unexplained full rebuild")
			}
			if m == nil {
				t.Fatal("manifest = nil; an unusable record must still yield an empty manifest to rebuild into")
			}
			if m.Len() != 0 {
				t.Errorf("Len = %d, want 0 — an untrusted record must not be applied", m.Len())
			}
			if m.Root != tc.root {
				t.Errorf("Root = %q, want the root it was opened for (%q)", m.Root, tc.root)
			}
		})
	}
}

// TestManifest_StatUnchangedNeedsEveryCheapFactToMatch pins FR-033's cheap half.
// A file counts as unchanged only when kind, size AND mtime all match; any one
// of them moving means the file must be re-parsed.
//
// The direction that matters is asymmetric: calling an identical file "changed"
// costs a needless re-parse, while calling a changed file "unchanged" leaves the
// index permanently, silently wrong.
func TestManifest_StatUnchangedNeedsEveryCheapFactToMatch(t *testing.T) {
	recorded := ManifestEntry{
		Path: "a.md", Kind: ScanKindNote, Size: 100, ModTimeNanos: 5000, Hash: "h", Segments: 1,
	}
	m := NewManifest("/root")
	m.Put(recorded)

	cases := []struct {
		name  string
		entry ScanEntry
		want  bool
	}{
		{"identical", ScanEntry{RelPath: "a.md", Kind: ScanKindNote, Size: 100, ModTimeNanos: 5000}, true},
		{"size changed", ScanEntry{RelPath: "a.md", Kind: ScanKindNote, Size: 101, ModTimeNanos: 5000}, false},
		{"mtime changed", ScanEntry{RelPath: "a.md", Kind: ScanKindNote, Size: 100, ModTimeNanos: 5001}, false},
		{"kind changed", ScanEntry{RelPath: "a.md", Kind: ScanKindAttachment, Size: 100, ModTimeNanos: 5000}, false},
		{"never recorded", ScanEntry{RelPath: "b.md", Kind: ScanKindNote, Size: 100, ModTimeNanos: 5000}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := m.StatUnchanged(tc.entry); got != tc.want {
				t.Errorf("StatUnchanged(%+v) = %v, want %v (FR-033: size, mtime or hash changed ⇒ re-parse)",
					tc.entry, got, tc.want)
			}
		})
	}
}

// TestManifest_SavedOwnerOnly asserts FR-032 on the manifest itself: it lists
// every path in the operator's collection, so it is 0600 inside a 0700
// directory, not whatever the process umask happens to allow.
func TestManifest_SavedOwnerOnly(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	dir := filepath.Join(t.TempDir(), "nested", "index")
	path := filepath.Join(dir, ManifestFileName)

	m := NewManifest("/root")
	m.Put(ManifestEntry{Path: "a.md", Kind: ScanKindNote, Size: 1, ModTimeNanos: 1, Segments: 1})
	if err := m.Save(path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != 0o600 {
		t.Errorf("manifest mode = %04o, want 0600 (FR-032)", got)
	}
	di, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != 0o700 {
		t.Errorf("manifest dir mode = %04o, want 0700 (FR-032)", got)
	}
}

// TestManifest_SavedFormIsDeterministic supports FR-046: the same collection,
// reconciled twice, must produce the same record. encoding/json sorts map keys,
// so this holds as long as nothing time- or order-dependent leaks into the file.
func TestManifest_SavedFormIsDeterministic(t *testing.T) {
	build := func() []byte {
		t.Helper()
		m := NewManifest("/root")
		for _, p := range []string{"z.md", "a.md", "m/x.md", "b.png"} {
			m.Put(ManifestEntry{Path: p, Kind: ScanKindFor(p), Size: 3, ModTimeNanos: 7, Segments: 1})
		}
		path := filepath.Join(t.TempDir(), ManifestFileName)
		if err := m.Save(path); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path) //nolint:gosec // test-local path
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	first, second := build(), build()
	if string(first) != string(second) {
		t.Errorf("manifest bytes differ between two identical saves:\n%s\n---\n%s", first, second)
	}
	var probe Manifest
	if err := json.Unmarshal(first, &probe); err != nil {
		t.Fatalf("saved manifest is not valid JSON: %v", err)
	}
}
