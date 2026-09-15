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

// TestLayout_CardsIsCarriedAndNeverSilentlyATable is FR-109's whole point,
// stated as the failure it prevents.
//
// The measured failure: an Obsidian CARDS view imported as a table, recorded
// NO loss at all, and scored CLEAN — a green number over an undetected loss.
// The view format carries a `layout` field and the product renders cards (the
// contract says so in as many words: "ONLY table AND cards ARE RENDERED"), so
// the honest outcome is that the layout is CARRIED. Clean is therefore correct
// here — but ONLY on the condition this test enforces: the written file must
// actually say `layout: cards`. A clean import with no layout key is the
// original failure wearing a new number.
func TestLayout_CardsIsCarriedAndNeverSilentlyATable(t *testing.T) {
	vo, written := layoutVault(t, "cards")

	if !strings.Contains(written, "layout: cards") {
		t.Fatalf("a CARDS view was imported without `layout: cards` in the file — this is the exact failure FR-109 exists to prevent. Written file:\n%s", written)
	}
	if vo.Layout != "cards" {
		t.Errorf("the report records layout %q for a cards view", vo.Layout)
	}
	for _, l := range vo.Losses {
		if pos, ok := parseLossPosition(l); ok && pos == LossLayout {
			t.Errorf("a cards view recorded a layout loss even though the layout was carried: %q", l)
		}
	}
	if vo.Disabled {
		t.Errorf("a CARDS view was DISABLED. A layout changes how rows are DRAWN, never which rows exist. Disabling losses: %v", vo.DisablingLosses)
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
	// `table`, `cards`, `board`, `calendar` and `gallery` are deliberately
	// absent: records.viewPartForLayout maps every one of them to a part
	// ViewPartsRenderer.tsx actually draws (table/columns/calendar/tiles), so
	// each is carried rather than named as a loss. `board` and `calendar`
	// have their own test (TestLayout_RenderedLayoutsCarryNoLossNote) — this
	// is the S3 regression: the importer used to name them as losses anyway.
	// Only `map` (declared but no part exists for it) and `list` (not a
	// declared layout at all, an Obsidian-only value) remain unrenderable.
	for _, layout := range []string{"map", "list"} {
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

// TestLayout_RenderedLayoutsCarryNoLossNote is the S3 regression fix
// (2026-09-14, TRIAGE-view-tabs-crosstab.md item 3): the `.base` re-derive
// path wrote a false "will be drawn as a table" note into a board view even
// though records.viewPartForLayout maps board to the `columns` part and
// ViewPartsRenderer.tsx draws it (and U-29 confirms boards render in the
// UI). The bug was two independent stale lists — view_write.go's
// `renderedLayouts` and knowledge_describe.go's `viewLayoutIsRendered` —
// that named only table and cards, never updated when board, calendar and
// gallery gained parts. Both now defer to records.ViewLayoutIsRendered.
//
// `map` is excluded here on purpose: it is the one layout
// records.viewPartForLayout genuinely has no part for, and it keeps its own
// coverage in TestLayout_UnrenderableLayoutsAreNamedIndividually above.
func TestLayout_RenderedLayoutsCarryNoLossNote(t *testing.T) {
	for _, layout := range []string{"table", "cards", "board", "calendar", "gallery"} {
		t.Run(layout, func(t *testing.T) {
			vo, written := layoutVault(t, layout)
			for _, l := range vo.Losses {
				if pos, ok := parseLossPosition(l); ok && pos == LossLayout {
					t.Errorf("layout %q is drawn by the SPA (records.viewPartForLayout says so) but the importer recorded a layout loss for it anyway: %q", layout, l)
				}
			}
			if layout != "table" {
				want := "layout: " + layout
				if !strings.Contains(written, want) {
					t.Errorf("layout %q should be carried verbatim into the written file (%q), got:\n%s", layout, want, written)
				}
			}
		})
	}
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
