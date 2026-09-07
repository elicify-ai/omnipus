// Tests for the knowledge-base marker: display name and template location
// (FR-022, FR-023, FR-024).
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// --- FR-024 -----------------------------------------------------------------

// TestMarker_StoresDisplayNameAndTemplateLocation.
//
// Oracle: FR-024 — "the system MUST store a knowledge base's display name and
// its template location in its marker" — and ADR-067 D2, "the marker holds a
// name and a templates/ directory, and nothing else".
func TestMarker_StoresDisplayNameAndTemplateLocation(t *testing.T) {
	home := t.TempDir()

	t.Run("default template location", func(t *testing.T) {
		c, err := CreateInWorkspace(home, "ws-default", "kb", Marker{DisplayName: "Research"})
		if err != nil {
			t.Fatalf("CreateInWorkspace: %v", err)
		}
		m, err := ReadMarker(c.Root())
		if err != nil {
			t.Fatalf("ReadMarker: %v", err)
		}
		if m.DisplayName != "Research" {
			t.Errorf("DisplayName = %q, want %q (FR-024)", m.DisplayName, "Research")
		}
		want := filepath.Join(c.Root(), MarkerDirName, DefaultTemplatesDirName)
		if got := TemplatesPath(c.Root(), m); got != want {
			t.Errorf("TemplatesPath = %q, want %q (ADR-067 D12: templates live in %s/%s)",
				got, want, MarkerDirName, DefaultTemplatesDirName)
		}
		if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
			t.Errorf("templates directory not created at %s: %v", want, err)
		}
	})

	t.Run("operator-named template location", func(t *testing.T) {
		c, err := CreateInWorkspace(home, "ws-custom", "kb", Marker{
			DisplayName:  "Research",
			TemplatesDir: "my-templates/notes",
		})
		if err != nil {
			t.Fatalf("CreateInWorkspace: %v", err)
		}
		m, err := ReadMarker(c.Root())
		if err != nil {
			t.Fatalf("ReadMarker: %v", err)
		}
		want := filepath.Join(c.Root(), MarkerDirName, "my-templates", "notes")
		if got := TemplatesPath(c.Root(), m); got != want {
			t.Errorf("TemplatesPath = %q, want %q", got, want)
		}
		if got := c.TemplatesDir(); got != want {
			t.Errorf("Collection.TemplatesDir() = %q, want %q", got, want)
		}
		if fi, err := os.Stat(want); err != nil || !fi.IsDir() {
			t.Errorf("templates directory not created at %s: %v", want, err)
		}
	})
}

// TestMarker_ValidateRejects covers every value the marker must refuse.
//
// Oracle: FR-024 (a name that can be stored and displayed), FR-025/FR-043 (a
// template location that stays inside the collection), and the project rule
// that control characters never reach a path or a log line
// (pkg/library/root.go's CleanRelPath, ADR-067 FR-0002a).
func TestMarker_ValidateRejects(t *testing.T) {
	cases := []struct {
		name   string
		marker Marker
	}{
		{"empty display name", Marker{DisplayName: ""}},
		{"whitespace-only display name", Marker{DisplayName: "   "}},
		{"newline in display name", Marker{DisplayName: "Research\nInjected: header"}},
		{"carriage return in display name", Marker{DisplayName: "Research\rX"}},
		{"NUL in display name", Marker{DisplayName: "Research\x00"}},
		{"absolute templates dir", Marker{DisplayName: "Research", TemplatesDir: "/etc"}},
		{"traversing templates dir", Marker{DisplayName: "Research", TemplatesDir: "../../etc"}},
		{"dot-dot prefixed templates dir", Marker{DisplayName: "Research", TemplatesDir: "..sneaky"}},
		{"backslash templates dir", Marker{DisplayName: "Research", TemplatesDir: `..\etc`}},
		{"templates dir is the marker dir itself", Marker{DisplayName: "Research", TemplatesDir: "."}},
		{"NUL in templates dir", Marker{DisplayName: "Research", TemplatesDir: "tpl\x00"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.marker.Validate(); !errors.Is(err, ErrMarkerInvalid) {
				t.Fatalf("Validate() = %v, want ErrMarkerInvalid", err)
			}
			// The same value must be refused at CREATE time, not only by the
			// standalone validator — otherwise the guard is unreachable from
			// the only path that writes a marker.
			home := t.TempDir()
			if _, err := CreateInWorkspace(home, "ws", "kb", tc.marker); err == nil {
				t.Errorf("CreateInWorkspace accepted an invalid marker %+v", tc.marker)
			} else if entries, readErr := os.ReadDir(home); readErr == nil && len(entries) != 0 {
				t.Errorf("a refused create left %d entries under home, want 0", len(entries))
			}
		})
	}
}

// TestMarker_ValidateAccepts guards against a validator so strict it refuses
// ordinary operator input — the failure mode a "rejects bad values" test alone
// cannot see (a Validate that always errors would pass the table above).
func TestMarker_ValidateAccepts(t *testing.T) {
	cases := []Marker{
		{DisplayName: "Research"},
		{DisplayName: "Ünïcödé — Näme"},
		{DisplayName: "Meeting: 2026-01-01"},
		{DisplayName: "Why?"},
		{DisplayName: "Research", TemplatesDir: "templates"},
		{DisplayName: "Research", TemplatesDir: "nested/templates"},
		{DisplayName: "Research", TemplatesDir: ".hidden-templates"},
	}
	for _, m := range cases {
		if err := m.Validate(); err != nil {
			t.Errorf("Validate(%+v) = %v, want nil", m, err)
		}
	}
}

// TestReadMarker_MalformedIsReportedNotDefaulted.
//
// Oracle: a collection whose identity cannot be read must say so. Silently
// defaulting to an empty display name would rename the operator's collection and
// report success — the "a control that reports success and changes nothing"
// pattern this project has had to correct before
// (docs/internal/false-green-patterns.md).
func TestReadMarker_MalformedIsReportedNotDefaulted(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"not json", "this is not json"},
		{"truncated json", `{"display_name": "Resea`},
		{"empty file", ""},
		{"json but no display name", `{}`},
		{"json with blank display name", `{"display_name":"   "}`},
		{"json with escaping templates dir", `{"display_name":"Research","templates_dir":"../../etc"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			mustMkdir(t, MarkerDir(root))
			mustWrite(t, MarkerPath(root), tc.body)

			m, err := ReadMarker(root)
			if err == nil {
				t.Fatalf("ReadMarker = %+v, nil; want an error", m)
			}
			if !errors.Is(err, ErrMarkerInvalid) {
				t.Errorf("ReadMarker error = %v, want ErrMarkerInvalid", err)
			}

			// OpenCollection must propagate it: a knowledge base whose marker is
			// corrupt must not open under a silently invented name.
			if c, err := OpenCollection(root); err == nil {
				t.Errorf("OpenCollection opened a collection with a corrupt marker as %q", c.DisplayName())
			}
		})
	}
}

// TestReadMarker_AbsentIsErrNoMarkerNotACorruptOne.
//
// Oracle: FR-020 — .obsidian/ alone makes a folder a knowledge base, so "no
// Omnipus marker" is an ordinary state that must be distinguishable from "the
// marker is broken". They lead to different actions: write one, versus stop.
func TestReadMarker_AbsentIsErrNoMarkerNotACorruptOne(t *testing.T) {
	root := t.TempDir()
	mustMkdir(t, filepath.Join(root, ObsidianMarkerDirName))

	_, err := ReadMarker(root)
	if !errors.Is(err, ErrNoMarker) {
		t.Fatalf("ReadMarker on a vault with no Omnipus marker = %v, want ErrNoMarker", err)
	}
	if errors.Is(err, ErrMarkerInvalid) {
		t.Errorf("an absent marker must not report as an invalid one: %v", err)
	}
}

// --- FR-023 -----------------------------------------------------------------

// TestMarkerWrite_NeverCreatesObsidian.
//
// Oracle: FR-023 — "the system MUST NOT create .obsidian/". Asserted at the
// write primitive itself, not only through CreateInWorkspace, so a future second
// caller of writeMarkerInto inherits the guarantee.
func TestMarkerWrite_NeverCreatesObsidian(t *testing.T) {
	dir := t.TempDir()
	r, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("OpenRoot: %v", err)
	}
	defer func() { _ = r.Close() }()

	if err := writeMarkerInto(r, "", Marker{DisplayName: "Research"}); err != nil {
		t.Fatalf("writeMarkerInto: %v", err)
	}
	if fi, err := os.Stat(filepath.Join(dir, MarkerDirName)); err != nil || !fi.IsDir() {
		t.Errorf("%s/ not created (FR-022): %v", MarkerDirName, err)
	}
	for _, rel := range treeOf(t, dir) {
		if strings.Contains(rel, ObsidianMarkerDirName) {
			t.Errorf("writeMarkerInto created %q (FR-023)", rel)
		}
	}
}

// --- Marker file posture ----------------------------------------------------

// TestMarkerWrite_OwnerOnlyPermissions guards this package's own documented
// decision (marker.go: markerDirPerm/markerFilePerm), not a spec clause. The
// marker is Omnipus state inside the operator's folder and will grow to hold
// collection settings, so it is created owner-only, matching FR-032's posture
// for the index.
func TestMarkerWrite_OwnerOnlyPermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits are not meaningful on Windows")
	}
	c, err := CreateInWorkspace(t.TempDir(), "ws", "kb", Marker{DisplayName: "Research"})
	if err != nil {
		t.Fatalf("CreateInWorkspace: %v", err)
	}
	checks := []struct {
		path string
		want fs.FileMode
	}{
		{MarkerDir(c.Root()), 0o700},
		{c.TemplatesDir(), 0o700},
		{MarkerPath(c.Root()), 0o600},
	}
	for _, ch := range checks {
		fi, err := os.Stat(ch.path)
		if err != nil {
			t.Errorf("stat %s: %v", ch.path, err)
			continue
		}
		if got := fi.Mode().Perm(); got != ch.want {
			t.Errorf("%s mode = %04o, want %04o", ch.path, got, ch.want)
		}
	}
}
