package pdf

// #1968 FIX6 (SEC2-2, G2-2). Parser-level regression tests with SMALL work and
// text budgets, so a missing meter shows up as a missing ErrWorkLimit or a
// lost text prefix instead of needing a multi-minute hostile run. Expected
// values come from fixture arithmetic and the Limits contract (every unit of
// decoding or lookup work is charged; a text-budget stop keeps the prefix),
// never from recorded parser output.

import (
	"bytes"
	"compress/zlib"
	"encoding/ascii85"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
)

func fix6Assemble(bodies []string) []byte {
	var buf bytes.Buffer
	buf.WriteString("%PDF-1.4\n")
	offs := make([]int, len(bodies))
	for i, b := range bodies {
		offs[i] = buf.Len()
		fmt.Fprintf(&buf, "%d 0 obj\n%s\nendobj\n", i+1, b)
	}
	xref := buf.Len()
	fmt.Fprintf(&buf, "xref\n0 %d\n0000000000 65535 f \n", len(bodies)+1)
	for _, o := range offs {
		fmt.Fprintf(&buf, "%010d 00000 n \n", o)
	}
	fmt.Fprintf(&buf, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n", len(bodies)+1, xref)
	return buf.Bytes()
}

func fix6Stream(dictExtra string, data []byte) string {
	return fmt.Sprintf("<< /Length %d %s >>\nstream\n%s\nendstream", len(data), dictExtra, data)
}

// fix6OnePage is a one-page document: font F1 with the ToUnicode program tu,
// content stream content (raw bytes) with the extra stream dictionary entries.
func fix6OnePage(tu string, content []byte, contentDict string) []byte {
	return fix6Assemble([]string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Count 1 /Kids [5 0 R] >>",
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /ToUnicode 4 0 R >>",
		fix6Stream("", []byte(tu)),
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 3 0 R >> >> /Contents 6 0 R >>",
		fix6Stream(contentDict, content),
	})
}

// fix6CodespaceTU declares misses codespace ranges that do not contain 'A'
// (0x41) and, when hit is true, a final range <41> <41> that does, plus the
// mapping A -> U+0041.
func fix6CodespaceTU(misses int, hit bool) string {
	var b strings.Builder
	n := misses
	if hit {
		n++
	}
	fmt.Fprintf(&b, "%d begincodespacerange\n", n)
	for i := 0; i < misses; i++ {
		b.WriteString("<01> <01>\n")
	}
	if hit {
		b.WriteString("<41> <41>\n")
	}
	b.WriteString("endcodespacerange\n1 beginbfchar\n<41> <0041>\nendbfchar\n")
	return b.String()
}

func fix6Read(t *testing.T, data []byte, lim Limits) (string, error) {
	t.Helper()
	r, err := NewReader(bytes.NewReader(data), int64(len(data)), lim)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	rd, err := r.GetPlainText()
	var text string
	if rd != nil {
		b, readErr := io.ReadAll(rd)
		if readErr != nil {
			t.Fatalf("reading kept text: %v", readErr)
		}
		text = string(b)
	}
	return text, err
}

// SEC2-2 scenario A: a codespace search that finds no range must spend work.
// 30,000 misses x 4,096 text bytes = 122,880,000 range examinations, far over
// the 8 MiB Work budget below (the cmap stream itself lexes in well under it).
func TestFix6_CodespaceMissSpendsWork(t *testing.T) {
	lim := Limits{Work: 8 << 20, Text: 1 << 20}

	t.Run("control: a range that matches decodes fully within the same budget", func(t *testing.T) {
		// Same 30,000 ranges, but the last one contains 'A', and a short
		// token: 16 x 30,001 = 480,016 examinations, inside 8 MiB.
		short := []byte("BT /F1 12 Tf (" + strings.Repeat("A", 16) + ") Tj ET")
		text, err := fix6Read(t, fix6OnePage(fix6CodespaceTU(30000, true), short, ""), lim)
		if err != nil {
			t.Fatalf("control must extract without a budget stop: %v", err)
		}
		if got, want := strings.TrimSpace(text), strings.Repeat("A", 16); got != want {
			t.Fatalf("control text = %q, want %q", got, want)
		}
	})
	t.Run("genuine misses are bounded by real cost, not by the limiter remainder", func(t *testing.T) {
		// Spec (FR-6 and the Limits contract): a miss-heavy document ends
		// with bounded CPU and allocation, by completing cheaply or by a
		// typed stop. The earlier oracle read the limiter remainder, which
		// counts the parser's own claims and lexer bytes, so an uncharged
		// linear scan satisfied it. The replacement is a fixture whose cost
		// grows with the ranges examined, judged by elapsed time, with the
		// miss proved against a reference scan (pdf_fix8_test.go).
		TestFix8_CodespaceMissLookupCostDoesNotScaleWithRanges(t)
	})
}

// fix6A85Chain is a content stream /Filter [/FlateDecode /ASCII85Decode]:
// inflating yields lead whitespace bytes, then the ASCII85 text of body.
func fix6A85Chain(t *testing.T, leadWhitespace int, body string) []byte {
	t.Helper()
	var z bytes.Buffer
	w, err := zlib.NewWriterLevel(&z, zlib.BestSpeed)
	if err != nil {
		t.Fatal(err)
	}
	chunk := bytes.Repeat([]byte{' '}, 1<<20)
	for left := leadWhitespace; left > 0; {
		n := min(left, len(chunk))
		if _, err := w.Write(chunk[:n]); err != nil {
			t.Fatal(err)
		}
		left -= n
	}
	enc := make([]byte, ascii85.MaxEncodedLen(len(body)))
	enc = enc[:ascii85.Encode(enc, []byte(body))]
	if _, err := w.Write(enc); err != nil {
		t.Fatal(err)
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return z.Bytes()
}

// SEC2-2 scenario B: intermediate filter output is work too. 200 MiB of
// whitespace between the two filters yields no final output, yet is 200 MiB
// of decoding, over the 64 MiB Work budget.
func TestFix6_FilterIntermediateOutputIsMetered(t *testing.T) {
	const tu = "1 begincodespacerange <00> <FF> endcodespacerange\n1 beginbfrange\n<00> <FF> <0000>\nendbfrange\n"
	const body = "BT /F1 12 Tf (hello) Tj ET"
	lim := Limits{Work: 64 << 20, Text: 1 << 20}
	dict := "/Filter [/FlateDecode /ASCII85Decode]"

	t.Run("control: one MiB of whitespace then real content", func(t *testing.T) {
		text, err := fix6Read(t, fix6OnePage(tu, fix6A85Chain(t, 1<<20, body), dict), lim)
		if err != nil {
			t.Fatalf("control must extract: %v", err)
		}
		if got := strings.TrimSpace(text); got != "hello" {
			t.Fatalf("control text = %q, want %q", got, "hello")
		}
	})
	t.Run("200 MiB intermediate expansion exhausts the budget", func(t *testing.T) {
		text, err := fix6Read(t, fix6OnePage(tu, fix6A85Chain(t, 200<<20, body), dict), lim)
		if !errors.Is(err, ErrWorkLimit) {
			t.Fatalf("SEC2-2: 200 MiB of intermediate filter output against a 64 MiB work budget must end with ErrWorkLimit; err=%v text=%q", err, text)
		}
	})
}

// G2-2 / SEC2-3 at the parser: the text-budget stop keeps the decoded prefix
// of the token it interrupted and still reports ErrTextLimit.
func TestFix6_TextBudgetStopKeepsDecodedPrefix(t *testing.T) {
	const tu = "1 begincodespacerange <00> <FF> endcodespacerange\n1 beginbfchar\n<41> <0041>\nendbfchar\n"
	// 1,000 one-byte results cannot fit a 600-byte text budget; BT spends 1.
	tj := []byte("BT /F1 12 Tf (" + strings.Repeat("A", 1000) + ") Tj ET")
	text, err := fix6Read(t, fix6OnePage(tu, tj, ""), Limits{Work: 1 << 28, Text: 600})
	if !errors.Is(err, ErrTextLimit) {
		t.Fatalf("a 600-byte text budget must stop 1,000 characters with ErrTextLimit, got %v", err)
	}
	body := strings.Trim(text, "\n")
	if strings.Trim(body, "A") != "" {
		t.Fatalf("kept text must be a prefix of the 'A' run, got %.40q", body)
	}
	if len(body) < 100 || len(body) > 599 {
		t.Fatalf("G2-2: the decoded prefix of the interrupted token must be kept: %d A's kept, want 100..599 of the 599 bytes that fit", len(body))
	}
}
