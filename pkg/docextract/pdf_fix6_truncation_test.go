package docextract

// #1968 FIX6 (SEC2-3, G2-2, SF-4). The generic extractor caps text at
// maxExtractRunes (100,000) runes and marks any cut with "\n…[truncated]"
// (extract.go contract); a result that is incomplete for ANY reason, parser
// budget stop included, must say so, and must keep the text that was read.
// InspectPDF keeps the partial text of a budget stop and appends its own
// marker. Expected values are fixture arithmetic, not recorded output.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pdfFix6Marker = "…[truncated]"

// pdfFix6OnlyA reports whether s, with the marker and newlines removed, is
// a run of 'A' only, returning its length.
func pdfFix6OnlyA(s string) (int, bool) {
	body := strings.TrimSuffix(strings.TrimRight(s, "\n"), pdfFix6Marker)
	body = strings.Trim(body, "\n")
	return len(body), strings.Trim(body, "A") == ""
}

// pdfFix6Extractors runs one PDF through every generic entry point.
func pdfFix6Extractors(t *testing.T, data []byte) map[string]func() (string, bool, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "doc.pdf")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return map[string]func() (string, bool, string){
		"ExtractBytes": func() (string, bool, string) { return ExtractBytes(data, "application/pdf", "doc.pdf") },
		"Extract":      func() (string, bool, string) { return Extract(path, "application/pdf", "doc.pdf") },
	}
}

// SEC2-3 / SF-4: A->U+0041, two Tj strings of 90,000 'A' each = 180,000 known
// characters, over the 100,000-rune cap. The result must carry the marker and
// the text that fits the cap; a silent 90,000-character "complete" result is
// the defect.
func TestPDFFix6_TwoLongStringsOverCapAreMarked(t *testing.T) {
	a := strings.Repeat("A", 90_000)
	content := []byte("BT /F1 12 Tf (" + a + ") Tj (" + a + ") Tj ET")
	data := pdfFix6Doc(pdfFix6MapA("0041"), [][]byte{content}, "")
	for name, run := range pdfFix6Extractors(t, data) {
		t.Run(name, func(t *testing.T) {
			text, ok, reason := run()
			if !ok {
				t.Fatalf("readable text must not be dropped: reason=%q", reason)
			}
			n, onlyA := pdfFix6OnlyA(text)
			if !onlyA {
				t.Fatalf("unexpected characters in %.60q", text)
			}
			if !strings.HasSuffix(text, "\n"+pdfFix6Marker) {
				t.Fatalf("SEC2-3: 180,000 known characters exceed the 100,000-rune cap, so the result must end with the truncation marker; got %d A's, tail %q", n, text[max(0, len(text)-30):])
			}
			// The whole first string is allowed text (90,000 < 100,000) and
			// the cap is 100,000 runes including the line break BT draws.
			if n < 90_000 || n > 100_000 {
				t.Fatalf("kept %d A's, want 90,000..100,000", n)
			}
		})
	}
	t.Run("control: 99,000 characters are complete and unmarked", func(t *testing.T) {
		c := []byte("BT /F1 12 Tf (" + strings.Repeat("A", 99_000) + ") Tj ET")
		for name, run := range pdfFix6Extractors(t, pdfFix6Doc(pdfFix6MapA("0041"), [][]byte{c}, "")) {
			text, ok, reason := run()
			n, onlyA := pdfFix6OnlyA(text)
			if !ok || !onlyA || n != 99_000 || strings.Contains(text, "[truncated]") {
				t.Fatalf("%s: ok=%t reason=%q A's=%d only-A=%t marker=%t", name, ok, reason, n, onlyA, strings.Contains(text, "[truncated]"))
			}
		}
	})
	t.Run("one 100,500-character string is kept to the cap and marked", func(t *testing.T) {
		c := []byte("BT /F1 12 Tf (" + strings.Repeat("A", 100_500) + ") Tj ET")
		for name, run := range pdfFix6Extractors(t, pdfFix6Doc(pdfFix6MapA("0041"), [][]byte{c}, "")) {
			text, ok, _ := run()
			n, onlyA := pdfFix6OnlyA(text)
			if !ok || !onlyA || n < 99_000 || !strings.HasSuffix(text, "\n"+pdfFix6Marker) {
				t.Fatalf("%s: ok=%t A's=%d only-A=%t tail=%q", name, ok, n, onlyA, text[max(0, len(text)-30):])
			}
		}
	})
}

// G2-2: one glyph maps to four characters; one 30,000-glyph Tj is 120,000
// known 'A' characters. The decoded prefix must survive the text-budget stop
// (not "no extractable text"), with or without earlier text.
func TestPDFFix6_MultiCharGlyphStringKeepsPrefix(t *testing.T) {
	tu := pdfFix6MapA("0041004100410041")
	glyphs := strings.Repeat("A", 30_000)
	cases := map[string]struct {
		content string
		prefix  string // text expected before the A run
	}{
		"alone":              {"BT /F1 12 Tf (" + glyphs + ") Tj ET", ""},
		"after earlier text": {"BT /F2 12 Tf (HELLO) Tj /F1 12 Tf (" + glyphs + ") Tj ET", "HELLO"},
	}
	for label, tc := range cases {
		data := pdfFix6Doc(tu, [][]byte{[]byte(tc.content)}, "")
		for name, run := range pdfFix6Extractors(t, data) {
			t.Run(label+"/"+name, func(t *testing.T) {
				text, ok, reason := run()
				if !ok {
					t.Fatalf("G2-2: 120,000 readable characters must not become a failure: reason=%q", reason)
				}
				rest := strings.TrimPrefix(strings.TrimLeft(text, "\n"), tc.prefix)
				n, onlyA := pdfFix6OnlyA(rest)
				if !onlyA {
					t.Fatalf("unexpected content after the prefix %q: %.60q", tc.prefix, rest)
				}
				if !strings.HasPrefix(strings.TrimLeft(text, "\n"), tc.prefix) {
					t.Fatalf("earlier text %q lost: %.40q", tc.prefix, text)
				}
				if n < 90_000 || n > 100_000 {
					t.Fatalf("G2-2: kept %d A's of the 120,000, want the 100,000-rune cap's worth (90,000..100,000)", n)
				}
				if !strings.HasSuffix(text, "\n"+pdfFix6Marker) {
					t.Fatalf("120,000 characters over the 100,000-rune cap must carry the marker; tail %q", text[max(0, len(text)-30):])
				}
			})
		}
	}
}

// G2-2 at InspectPDF: 30,000 glyphs, each mapping to 256 'A' = 7,680,000 known
// characters in one string, below the 25 MiB text contract. Text must not be
// lost; if the parser stops early the partial text is kept and marked.
func TestPDFFix6_InspectKeepsLargeMappedString(t *testing.T) {
	const total = 30_000 * 256
	tu := pdfFix6MapA(strings.Repeat("0041", 256))
	content := []byte("BT /F1 12 Tf (" + strings.Repeat("A", 30_000) + ") Tj ET")
	info, err := InspectPDF(pdfFix6Doc(tu, [][]byte{content}, ""))
	if err != nil {
		t.Fatalf("G2-2: readable text must not be reported as a malformed document: %v", err)
	}
	n, onlyA := pdfFix6OnlyA(info.Text)
	if !onlyA {
		t.Fatalf("unexpected characters in %.60q", info.Text)
	}
	marked := strings.HasSuffix(info.Text, pdfTruncatedMarker)
	switch {
	case n == total && !marked: // complete and unmarked
	case marked && n >= 1<<20 && n < total: // cut, kept and marked
	default:
		t.Fatalf("want all %d characters unmarked, or a marked prefix of at least 1 MiB; got %d characters, marked=%t", total, n, marked)
	}
}

// The valid multi-page control for the truncation family.
func TestPDFFix6_ThreePageControlCompleteEverywhere(t *testing.T) {
	data := pdfFix6ThreePageControl()
	// Page text is compared with all white space removed: the legacy
	// extractor joins pages without a separator.
	want := "PAGE1textPAGE2textPAGE3text"
	squash := func(s string) string { return strings.Join(strings.Fields(s), "") }
	info, err := InspectPDF(data)
	if err != nil || info.Pages != 3 || squash(info.Text) != want || strings.Contains(info.Text, "[truncated]") {
		t.Fatalf("InspectPDF control: pages=%d err=%v text=%q", info.Pages, err, info.Text)
	}
	for name, run := range pdfFix6Extractors(t, data) {
		text, ok, reason := run()
		if !ok || squash(text) != want || strings.Contains(text, "[truncated]") {
			t.Fatalf("%s control: ok=%t reason=%q text=%q", name, ok, reason, text)
		}
	}
}
