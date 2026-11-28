package agent

// #1968 FIX6 (SEC2-3, G2-2, SF-4) as the model sees it. A chat PDF larger
// than the operator's max_media_size reaches the model as the legacy
// extractor's text (encodeChatPDF's second return value is the exact text
// appended to the user message). Text cut by the 100,000-rune cap or by a
// parser budget must carry the truncation marker; readable text must not be
// replaced by a failure notice. Oracles: fixture arithmetic and the
// established cap/marker contract.

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const pdfFix6Marker = "…[truncated]"

func pdfFix6OverMedia(t *testing.T, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "big.pdf")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	url, injection := encodeChatPDF(path, "application/pdf", "big.pdf", int64(len(data)), len(data)-1, &chatPDFRegistry{})
	if url != "" {
		t.Fatalf("over max_media_size must not be sent natively, got a data URL")
	}
	return injection
}

// pdfFix6CountA counts the fixture's 'A' characters, not the capital A of the
// attachment header.
func pdfFix6CountA(s string) int {
	return strings.Count(strings.ReplaceAll(s, "[Attached file", "[ttached file"), "A")
}

func TestChatPDFFix6_OverMediaSizeTextIsMarkedAndKept(t *testing.T) {
	a := strings.Repeat("A", 90_000)
	cases := map[string][]byte{
		// SEC2-3: 2 x 90,000 = 180,000 known characters over the 100,000 cap.
		"two 90,000-character strings": pdfFix6Doc(pdfFix6MapA("0041"), [][]byte{[]byte("BT /F1 12 Tf (" + a + ") Tj (" + a + ") Tj ET")}, ""),
		// G2-2: 30,000 glyphs x 4 characters = 120,000 known characters.
		"one 30,000-glyph string mapping to 4 characters each": pdfFix6Doc(pdfFix6MapA("0041004100410041"), [][]byte{[]byte("BT /F1 12 Tf (" + strings.Repeat("A", 30_000) + ") Tj ET")}, ""),
	}
	for label, data := range cases {
		t.Run(label, func(t *testing.T) {
			got := pdfFix6OverMedia(t, data)
			if strings.Contains(got, "could not be extracted") {
				t.Fatalf("readable text became a failure notice: %.200q", got)
			}
			if !strings.Contains(got, "[Attached file \"big.pdf\"]") {
				t.Fatalf("no attached-text block: %.200q", got)
			}
			if n := pdfFix6CountA(got); n < 90_000 || n > 100_000 {
				t.Fatalf("model sees %d A's, want 90,000..100,000", n)
			}
			if !strings.Contains(got, "\n"+pdfFix6Marker+"\n[End of attached file]") {
				t.Fatalf("SEC2-3: text over the 100,000-rune cap must end with the truncation marker before the end-of-file line; tail %q", got[max(0, len(got)-120):])
			}
		})
	}
	t.Run("control: a complete multi-page PDF over max_media_size is unmarked", func(t *testing.T) {
		got := pdfFix6OverMedia(t, pdfFix6ThreePageControl())
		start := strings.Index(got, "[Attached file")
		end := strings.Index(got, "[End of attached file]")
		if start < 0 || end < start {
			t.Fatalf("no attached-text block: %.200q", got)
		}
		body := strings.TrimPrefix(strings.TrimSpace(got[start:end]), "[Attached file \"big.pdf\"]")
		if strings.Join(strings.Fields(body), "") != "PAGE1textPAGE2textPAGE3text" || strings.Contains(got, "[truncated]") {
			t.Fatalf("control text = %q", got)
		}
	})
}

// G2-2 through the in-budget chat inspection (InspectPDF + CapText): 7,680,000
// known characters must reach the model as text capped and marked, never as a
// "could not be read" notice.
func TestChatPDFFix6_InspectedLargeMappedStringIsPresented(t *testing.T) {
	tu := pdfFix6MapA(strings.Repeat("0041", 256))
	data := pdfFix6Doc(tu, [][]byte{[]byte("BT /F1 12 Tf (" + strings.Repeat("A", 30_000) + ") Tj ET")}, "")
	c := &chatPDF{name: "big.pdf"}
	c.inspect("data:application/pdf;base64," + base64.StdEncoding.EncodeToString(data))
	if !c.hasText || strings.Contains(c.injection, "could not be read") {
		t.Fatalf("G2-2: readable text lost; hasText=%t injection=%.200q", c.hasText, c.injection)
	}
	if n := pdfFix6CountA(c.injection); n < 90_000 || n > 100_000 {
		t.Fatalf("model sees %d A's, want the 100,000-rune cap's worth", n)
	}
	if !strings.Contains(c.injection, pdfFix6Marker+"\n[End of attached file]") {
		t.Fatalf("capped text must carry the marker; tail %q", c.injection[max(0, len(c.injection)-120):])
	}
}
