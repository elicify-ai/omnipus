// Omnipus — regression coverage for the 2026-09-13 UAT finding D-129 (and
// the text-index half of D-99): `zurich` found "Zürich" (by the fuzzy
// fallback, not by folding) while `cafe` did not find "Café" and `resume`
// did not find "résumé"; an NFC "café.png" and an NFD "café (1).png" —
// identical on screen — answered to different queries; and the coverage
// numbers counted attachments as searched notes.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package knowledge

import (
	"context"
	"testing"
)

// TestUAT_D129_AccentedAndPlainSpellingsReachTheSameFiles — every spelling a
// person actually types reaches every file that carries the word, on the
// exact AND tier, never by the fuzzy fallback.
func TestUAT_D129_AccentedAndPlainSpellingsReachTheSameFiles(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "Assets/café résumé.md", "# Café résumé\n\nA résumé kept at the café.\n")
	b2WriteFile(t, root, "Records/Café Zürich telemetry.md", "# Café Zürich\n\nTelemetry from Zürich.\n")
	b2WriteFile(t, root, "Dashboards/café.png", "png bytes\n")      // NFC name
	b2WriteFile(t, root, "Dashboards/café (1).png", "png bytes\n") // NFD name (macOS)
	b2WriteFile(t, root, "Other/unrelated.md", "# Nothing here\n\nplain words only\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	cafeFiles := []string{
		"Assets/café résumé.md",
		"Records/Café Zürich telemetry.md",
		"Dashboards/café.png",
		"Dashboards/café (1).png",
	}
	for _, q := range []string{"cafe", "café", "café", "CAFÉ"} {
		hits, _, fellBack, err := ix.SearchFiltered(q, 20, nil)
		if err != nil {
			t.Fatal(err)
		}
		if fellBack {
			t.Errorf("D-129: %q must match on the exact tier, not the fuzzy fallback", q)
		}
		for _, want := range cafeFiles {
			if !containsPath(hits, want) {
				t.Errorf("D-129: %q must reach %q, got %v", q, want, b2HitPaths(hits))
			}
		}
		if containsPath(hits, "Other/unrelated.md") {
			t.Errorf("%q must not reach an unrelated note", q)
		}
	}

	for q, want := range map[string]string{
		"resume": "Assets/café résumé.md",
		"résumé": "Assets/café résumé.md",
		"zurich": "Records/Café Zürich telemetry.md",
		"Zürich": "Records/Café Zürich telemetry.md",
	} {
		hits, _, fellBack, err := ix.SearchFiltered(q, 20, nil)
		if err != nil {
			t.Fatal(err)
		}
		if fellBack {
			t.Errorf("D-129: %q must match on the exact tier, not the fuzzy fallback", q)
		}
		if !containsPath(hits, want) {
			t.Errorf("D-129: %q must reach %q, got %v", q, want, b2HitPaths(hits))
		}
	}

	// The per-word counts D-07 reports fold the same way, so "cafe" is never
	// reported as absent from a vault that spells it "café".
	counts, err := ix.TermDocumentCounts("cafe résumé")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range counts {
		if c.Documents == 0 {
			t.Errorf("D-129: term %q counted in 0 files, folding is not applied to the counts: %+v", c.Term, counts)
		}
	}
}

// TestUAT_D129_FreshnessCountsNotesApartFromFiles — the coverage pair a
// searcher is told to trust must count notes, not files.
func TestUAT_D129_FreshnessCountsNotesApartFromFiles(t *testing.T) {
	home, root := t.TempDir(), t.TempDir()
	b2WriteFile(t, root, "a.md", "# A\n")
	b2WriteFile(t, root, "b.md", "# B\n")
	b2WriteFile(t, root, "img/one.png", "png\n")
	b2WriteFile(t, root, "img/two.pdf", "pdf\n")
	ix := b2Open(t, home, root)
	b2Sync(t, ix)

	f, err := ix.Freshness(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if f.Scanned != 4 || f.Indexed != 4 {
		t.Fatalf("file counts: scanned %d indexed %d, want 4/4", f.Scanned, f.Indexed)
	}
	if f.ScannedNotes != 2 || f.IndexedNotes != 2 {
		t.Fatalf("D-129: note counts must exclude the two attachments: scanned %d indexed %d, want 2/2",
			f.ScannedNotes, f.IndexedNotes)
	}
}
