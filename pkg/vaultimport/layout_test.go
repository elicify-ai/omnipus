// Omnipus — FR-109: a view's LAYOUT is part of what must not be lost
// silently. The measured failure this closes: an Obsidian CARDS view
// imported as a table, recorded no loss, and scored CLEAN.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package vaultimport

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/records"
)

// layoutVault writes a one-type, one-base vault whose single view declares
// the given Obsidian `type:` (its LAYOUT), imports it, and returns the one
// produced view outcome plus the bytes written for it.
func layoutVault(t *testing.T, obsidianViewType string) (ViewOutcome, string) {
	t.Helper()
	root := t.TempDir()

	note := "---\ntype: task\nstatus: open\n---\n\nbody\n"
	if err := os.WriteFile(filepath.Join(root, "A task.md"), []byte(note), 0o644); err != nil {
		t.Fatalf("writing the fixture note: %v", err)
	}

	typeLine := ""
	if obsidianViewType != "" {
		typeLine = "    type: " + obsidianViewType + "\n"
	}
	base := "filters:\n  and:\n    - type == \"task\"\nviews:\n  - name: Everything\n" + typeLine
	if err := os.WriteFile(filepath.Join(root, "Tasks.base"), []byte(base), 0o644); err != nil {
		t.Fatalf("writing the fixture base: %v", err)
	}

	rep, err := Run(root, true)
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if len(rep.Bases) != 1 || len(rep.Bases[0].Views) != 1 {
		t.Fatalf("expected exactly one base with one view, got %+v", rep.Bases)
	}
	vo := rep.Bases[0].Views[0]
	if vo.Status == OutcomeRefused {
		t.Fatalf("the fixture view was refused, so nothing about layout can be measured: %s", vo.RefusedReason)
	}

	data, err := os.ReadFile(filepath.Join(records.ViewsDir(root), "tasks--everything.yaml"))
	if err != nil {
		t.Fatalf("reading the written view file: %v", err)
	}
	return vo, string(data)
}

// TestLayout_IsReadFromTheBase is the base fact the whole requirement rests
// on: the importer must OPEN the view's `type` key. Before FR-109 it never
// did, so every layout in the founder's vault was thrown away unread.
func TestLayout_IsReadFromTheBase(t *testing.T) {
	for _, want := range []string{"table", "cards", "board", "calendar", "gallery", "map", "list"} {
		t.Run(want, func(t *testing.T) {
			vo, _ := layoutVault(t, want)
			if vo.Layout != want {
				t.Errorf("the base declared layout %q; the importer recorded %q", want, vo.Layout)
			}
		})
	}
}

// TestLayout_CardsCannotImportClean is FR-109's whole point, stated as the
// failure it prevents.
//
// A cards view that imports as a table with NO loss recorded scores CLEAN
// under W7's exit criterion — a green number over an undetected loss, which
// is the precise failure ADR-068 is written against. So: a cards view MUST
// carry a named loss, and that loss MUST be an annotation (it changes how
// rows are drawn, never which rows there are), so the view stays ENABLED.
func TestLayout_CardsCannotImportClean(t *testing.T) {
	vo, written := layoutVault(t, "cards")

	if vo.Status == OutcomeConverted {
		t.Fatalf("a CARDS view imported CLEAN — this is the exact failure FR-109 exists to prevent. Written file:\n%s", written)
	}
	if len(vo.Losses) == 0 {
		t.Fatal("a CARDS view imported with no named loss at all")
	}

	var layoutLoss string
	for _, l := range vo.Losses {
		if pos, ok := parseLossPosition(l); ok && pos == LossLayout {
			layoutLoss = l
		}
	}
	if layoutLoss == "" {
		t.Fatalf("a CARDS view recorded losses but none at the layout position: %v", vo.Losses)
	}
	if !strings.Contains(layoutLoss, "cards") {
		t.Errorf("the layout loss does not name the layout that was asked for: %q", layoutLoss)
	}
	if vo.Disabled {
		t.Errorf("a CARDS view was DISABLED. A layout changes how rows are DRAWN, never which rows exist, so it is an annotation loss (FR-106) and the view must still be applicable. Disabling losses: %v", vo.DisablingLosses)
	}
}

// TestLayout_TableLosesNothing is the control that stops the test above from
// passing for the wrong reason. If EVERY layout produced a loss, the cards
// assertion would hold with the feature deleted — so the ordinary case must
// come out clean.
func TestLayout_TableLosesNothing(t *testing.T) {
	for _, declared := range []string{"table", ""} {
		name := declared
		if name == "" {
			name = "(no type key at all)"
		}
		t.Run(name, func(t *testing.T) {
			vo, written := layoutVault(t, declared)
			for _, l := range vo.Losses {
				if pos, ok := parseLossPosition(l); ok && pos == LossLayout {
					t.Errorf("a table view recorded a layout loss: %q", l)
				}
			}
			if vo.Status != OutcomeConverted {
				t.Errorf("a plain table view did not import clean (%s); losses: %v\nwritten:\n%s", vo.Status, vo.Losses, written)
			}
		})
	}
}

// TestLayout_UnrenderableLayoutsAreNamedIndividually checks the report says
// WHICH layout was asked for, not merely that one was. "This view wanted
// something we cannot draw" sends nobody anywhere; "this view wanted a
// calendar" is actionable.
func TestLayout_UnrenderableLayoutsAreNamedIndividually(t *testing.T) {
	for _, layout := range []string{"cards", "board", "calendar", "gallery", "map", "list"} {
		t.Run(layout, func(t *testing.T) {
			vo, _ := layoutVault(t, layout)
			var found bool
			for _, l := range vo.Losses {
				if pos, ok := parseLossPosition(l); ok && pos == LossLayout && strings.Contains(l, layout) {
					found = true
				}
			}
			if !found {
				t.Errorf("layout %q produced no loss naming it; losses were %v", layout, vo.Losses)
			}
		})
	}
}

// TestLayout_NeverWritesAKeyTheEmittedFormatHasNoField exists because the
// tempting fix — always write `layout:` — produces files that load today and
// are REJECTED the moment somebody gates v2-only keys by version, long after
// the run that wrote them.
//
// The assertion is stated in terms of the emitted version, so it stays true
// on both sides of that change rather than needing to be rewritten when
// pkg/records starts emitting version 2.
func TestLayout_NeverWritesAKeyTheEmittedFormatHasNoField(t *testing.T) {
	_, written := layoutVault(t, "cards")
	hasKey := strings.Contains(written, "\nlayout:")

	switch {
	case emitsLayoutKey() && !hasKey:
		t.Errorf("the emitted view format is schema_version %d, which carries `layout`, but the written file does not:\n%s", records.SupportedViewVersion, written)
	case !emitsLayoutKey() && hasKey:
		t.Errorf("the emitted view format is schema_version %d, which has NO `layout` field, but the importer wrote one:\n%s", records.SupportedViewVersion, written)
	}
	t.Logf("emitted schema_version=%d, carries layout=%v", records.SupportedViewVersion, emitsLayoutKey())
}

// TestLayout_WrittenViewStillLoads is the round trip. Whatever the importer
// decides to write about layout, the real loader must accept it — a view
// this package writes and pkg/records rejects is worse than no view.
func TestLayout_WrittenViewStillLoads(t *testing.T) {
	for _, layout := range []string{"", "table", "cards", "calendar", "list"} {
		name := layout
		if name == "" {
			name = "(none)"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			note := "---\ntype: task\nstatus: open\n---\n\nbody\n"
			if err := os.WriteFile(filepath.Join(root, "A task.md"), []byte(note), 0o644); err != nil {
				t.Fatal(err)
			}
			typeLine := ""
			if layout != "" {
				typeLine = "    type: " + layout + "\n"
			}
			base := "filters:\n  and:\n    - type == \"task\"\nviews:\n  - name: Everything\n" + typeLine
			if err := os.WriteFile(filepath.Join(root, "Tasks.base"), []byte(base), 0o644); err != nil {
				t.Fatal(err)
			}
			if _, err := Run(root, true); err != nil {
				t.Fatalf("import failed: %v", err)
			}
			schemas, _, err := records.LoadSchemas(root)
			if err != nil {
				t.Fatalf("loading schemas: %v", err)
			}
			_, viewRep, err := records.LoadViews(root, schemas)
			if err != nil {
				t.Fatalf("loading views: %v", err)
			}
			if !viewRep.OK() {
				for _, rej := range viewRep.Rejections {
					t.Errorf("layout %q: the real loader rejected the view this importer wrote: %s", layout, rej.String())
				}
			}
		})
	}
}
