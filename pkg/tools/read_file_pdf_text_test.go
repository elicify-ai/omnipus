package tools

// Spec #1968 (read-file native PDF) — T6 and T8, tool level. Both are
// labelled A: they FAIL BY ASSERTION on today's code, where an ASCII PDF
// (no NUL in the first 512 sniffed bytes) is returned raw (`obj`/`stream`
// leak) and a compressed one is silently decoded to text with no notices.
//
// Oracles: spec §5.2 FR-5 (`.pdf` name or `%PDF-` magic routes to the PDF
// path before the NUL check), FR-8 (persisted-result table), §5.6 fixed
// notice content (header, R1, L1); §6 rows T6 and T8.

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// notice R1 content (§5.6, fixed): the COMPLETE native-send sentence —
// including the retention scope the SPA user must see (F14: partial
// "sent natively…" checks allowed notices that omitted the contract's
// "this turn only; later turns see this text" clause).
const pdfR1Full = "sent natively to models that support PDFs, within their limits, this turn only; later turns see this text"

// notice L1 content (§5.6, fixed): over the operator media size setting.
const pdfL1Sentence = "not sent natively"

// pdfPreviewLines is how many lines of a tool result the SPA preview shows
// before "show more" (FACT in the spec: FileReadBlock previews the first 20
// lines). Every notice must sit within them or the user never sees it.
const pdfPreviewLines = 20

// pdfAssertWithinPreviewLines requires every notice position to fall within
// the SPA preview's first pdfPreviewLines lines of the result (F14:
// preview position). Negative indexes mean the notice is missing entirely
// and fail here.
func pdfAssertWithinPreviewLines(t *testing.T, label, s string, idxs ...int) {
	t.Helper()
	for _, idx := range idxs {
		if idx < 0 {
			t.Fatalf("%s: a notice that must be visible is missing from the result:\n%s", label, clip(s))
		}
		line := 1 + strings.Count(s[:idx], "\n")
		if line > pdfPreviewLines {
			t.Fatalf("%s: notice sits on line %d — beyond the %d-line SPA preview (F14: the user would never see it):\n%s", label, line, pdfPreviewLines, clip(s))
		}
	}
}

// pdfAssertHeader verifies the persisted document header's VALUES (F14):
// `[document: <name> | extracted text | chars <off>-<end> of <n> | <pages> pages]`.
// pages < 0 requires the literal "unknown"; a length > 0 additionally pins
// the end character at off+length-1 (the exact requested window).
func pdfAssertHeader(t *testing.T, label, s, name string, pages int, rng ...int) {
	t.Helper()
	off, length := 0, -1
	if len(rng) > 0 {
		off = rng[0]
	}
	if len(rng) > 1 {
		length = rng[1]
	}
	pat := regexp.QuoteMeta(fmt.Sprintf("[document: %s | extracted text | chars %d-", name, off))
	m := regexp.MustCompile(pat + `(\d+) of (\d+) \| (\d+|unknown) pages\]`).FindStringSubmatch(s)
	if m == nil {
		t.Fatalf("%s: document header missing or wrong shape (want %q prefix + \"chars a-b of n | P pages\"):\n%s",
			label, fmt.Sprintf("[document: %s | extracted text | chars %d-", name, off), clip(s))
	}
	if length >= 0 {
		end, err := strconv.Atoi(m[1])
		if err != nil || end != off+length-1 {
			t.Fatalf("%s: header end char = %q, want %d (offset %d + length %d - 1)", label, m[1], off+length-1, off, length)
		}
	}
	if len(rng) > 2 {
		total, err := strconv.Atoi(m[2])
		if err != nil || total != rng[2] {
			t.Fatalf("%s: header total = %q, want %d extracted characters", label, m[2], rng[2])
		}
	}
	if m[3] == "unknown" {
		if pages >= 0 {
			t.Fatalf("%s: header says pages unknown, want %d", label, pages)
		}
	} else {
		got, err := strconv.Atoi(m[3])
		if err != nil || got != pages {
			t.Fatalf("%s: header pages = %q, want %d", label, m[3], pages)
		}
	}
}

// The body oracle is the displayed extracted text, with notice/framing
// lines removed. A full-read reference supplies the pagination domain;
// this is a metamorphic check, not an expectation copied from a paged read.
func pdfTextBody(result string) string {
	lines := strings.Split(result, "\n")
	var body []string
	for _, line := range lines {
		if strings.HasPrefix(line, "[document:") || strings.HasPrefix(line, "[PDF:") || strings.HasPrefix(line, "[PDF over ") {
			continue
		}
		body = append(body, line)
	}
	return strings.Join(body, "\n") // preserve extracted whitespace: it belongs to the character domain
}

func pdfAssertPagedWindow(t *testing.T, label, fullResult, pagedResult, name string, pages, offset, length int) {
	t.Helper()
	full := []rune(pdfTextBody(fullResult))
	require.GreaterOrEqual(t, len(full), offset+length, "%s: nonempty complete extraction premise", label)
	pdfAssertHeader(t, label+" full", fullResult, name, pages, 0, len(full), len(full))
	pdfAssertHeader(t, label+" paged", pagedResult, name, pages, offset, length, len(full))
	require.Equal(t, string(full[offset:offset+length]), pdfTextBody(pagedResult), "%s: EXACT returned character window, no suffix or raw syntax", label)
}

func TestPDF1968PagingOracle(t *testing.T) {
	full := "[document: report.pdf | extracted text | chars 0-22 of 23 | 2 pages]\n[PDF: " + pdfR1Full + "]\nPAGE 1 text\nPAGE 2 text"
	page := "[document: report.pdf | extracted text | chars 0-5 of 23 | 2 pages]\n[PDF: " + pdfR1Full + "]\nPAGE 1"
	pdfAssertPagedWindow(t, "oracle", full, page, "report.pdf", 2, 0, 6)
	require.Equal(t, "PAGE 1EXTRA", pdfTextBody(page+"EXTRA"), "the body parser must not hide excess characters")
	untrimmed := "[document: report.pdf | extracted text | chars 0-23 of 24 | 2 pages]\n[PDF: " + pdfR1Full + "]\n\nPAGE 1 text\nPAGE 2 text"
	untrimmedPage := "[document: report.pdf | extracted text | chars 0-5 of 24 | 2 pages]\n[PDF: " + pdfR1Full + "]\n\nPAGE "
	pdfAssertPagedWindow(t, "untrimmed oracle", untrimmed, untrimmedPage, "report.pdf", 2, 0, 6)
}

// writePDF writes fixture bytes under name in dir and fails the test on
// error.
func writePDF(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o600); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// clip bounds a tool result excerpt in failure messages — a raw-file view
// can be tens of kilobytes and must not flood the log.
func clip(s string) string {
	if len(s) <= 400 {
		return s
	}
	return s[:400] + "…[clipped]"
}

// TestReadFile_ASCIIAndCompressedPDF_Text (T6, A) — a `.pdf` file is routed
// to the PDF path by name or magic BEFORE the NUL check (FR-5), so both an
// ASCII PDF (NUL-free first 512 bytes) and a compressed one yield extracted
// text with the fixed header and R1, never raw PDF syntax.
func TestReadFile_ASCIIAndCompressedPDF_Text(t *testing.T) {
	t.Run("ASCII pdf is extracted with header and R1, not leaked raw", func(t *testing.T) {
		root := t.TempDir()
		data := buildTextPDF(t, "1.4", 2, true, nil)
		for i, b := range data[:512] {
			if b == 0 {
				t.Fatalf("fixture broken: NUL at byte %d (must stay NUL-free so today's code takes the raw path)", i)
			}
		}
		writePDF(t, root, "report.pdf", data)

		tool := NewReadFileTool(root, false, MaxReadFileSize)
		res := tool.Execute(context.Background(), map[string]any{"path": "report.pdf"})
		if res.IsError {
			t.Fatalf("text PDF read errored: %s", clip(res.ForLLM))
		}
		if !strings.Contains(res.ForLLM, "PAGE 1 text") {
			t.Fatalf("extracted text missing from result:\n%s", clip(res.ForLLM))
		}
		// FR-8 persisted header (§5.6, fixed content): document header with
		// verified range and page VALUES, not today's raw [file: …] header.
		headerIdx := strings.Index(res.ForLLM, "[document: report.pdf | extracted text | chars ")
		if headerIdx < 0 {
			t.Fatalf("fixed document header missing (got the raw-file view instead):\n%s", clip(res.ForLLM))
		}
		fullText := pdfTextBody(res.ForLLM)
		require.Regexp(t, `^\n?PAGE 1 text\nPAGE 2 text\n?$`, fullText, "complete fixture text; the spec does not mandate trimming extractor newlines")
		pdfAssertHeader(t, "ascii", res.ForLLM, "report.pdf", 2, 0, len([]rune(fullText)), len([]rune(fullText)))
		// §5.6 R1, COMPLETE fixed content: the native-send notice with its
		// retention scope appears in the persisted result, after the header
		// and before the body text.
		r1Idx := strings.Index(res.ForLLM, pdfR1Full)
		if r1Idx < 0 {
			t.Fatalf("full R1 notice missing (must include \"this turn only; later turns see this text\"):\n%s", clip(res.ForLLM))
		}
		bodyIdx := strings.Index(res.ForLLM, "PAGE 1 text")
		if r1Idx < headerIdx || bodyIdx < r1Idx {
			t.Fatalf("notices must precede the body (header@%d, R1@%d, body@%d)", headerIdx, r1Idx, bodyIdx)
		}
		// F14: both notices must sit inside the SPA's 20-line preview.
		pdfAssertWithinPreviewLines(t, "ascii", res.ForLLM, headerIdx, r1Idx)
		// The raw PDF syntax must never surface (S1: extracted text only).
		for _, marker := range []string{"1 0 obj", "endobj", "startxref", "trailer", "xref"} {
			if strings.Contains(res.ForLLM, marker) {
				t.Fatalf("raw PDF syntax %q leaked into the result:\n%s", marker, clip(res.ForLLM))
			}
		}
	})

	t.Run("compressed pdf keeps the notices too", func(t *testing.T) {
		root := t.TempDir()
		writePDF(t, root, "report2.pdf", buildCompressedPDF(t, "compressed hello"))

		tool := NewReadFileTool(root, false, MaxReadFileSize)
		res := tool.Execute(context.Background(), map[string]any{"path": "report2.pdf"})
		if res.IsError {
			t.Fatalf("compressed PDF read errored: %s", clip(res.ForLLM))
		}
		if !strings.Contains(res.ForLLM, "compressed hello") {
			t.Fatalf("extracted text missing from result:\n%s", clip(res.ForLLM))
		}
		// Today the document is silently decoded with no native-send notice;
		// FR-8 requires header + full R1 + text for a text PDF.
		if !strings.Contains(res.ForLLM, pdfR1Full) {
			t.Fatalf("full R1 notice missing for a compressed text PDF:\n%s", clip(res.ForLLM))
		}
	})

	t.Run("magic-only filename still routes to the PDF path", func(t *testing.T) {
		root := t.TempDir()
		// FR-5: a %PDF- magic routes to the PDF path even when the name is
		// not .pdf (the magic check must precede the NUL check and the name
		// check must not be the only trigger).
		writePDF(t, root, "report.bin", buildTextPDF(t, "1.4", 1, true, nil))

		tool := NewReadFileTool(root, false, MaxReadFileSize)
		res := tool.Execute(context.Background(), map[string]any{"path": "report.bin"})
		if res.IsError {
			t.Fatalf("magic-only PDF read errored: %s", clip(res.ForLLM))
		}
		if !strings.Contains(res.ForLLM, "PAGE 1 text") {
			t.Fatalf("magic-only PDF must be extracted as text:\n%s", clip(res.ForLLM))
		}
		if strings.Contains(res.ForLLM, "1 0 obj") {
			t.Fatalf("magic-only PDF leaked raw syntax:\n%s", clip(res.ForLLM))
		}
	})

	t.Run("character paging pages extracted characters, not raw bytes", func(t *testing.T) {
		root := t.TempDir()
		writePDF(t, root, "report.pdf", buildTextPDF(t, "1.4", 2, true, nil))

		tool := NewReadFileTool(root, false, MaxReadFileSize)
		full := tool.Execute(context.Background(), map[string]any{"path": "report.pdf"})
		require.False(t, full.IsError)
		require.Regexp(t, `^\n?PAGE 1 text\nPAGE 2 text\n?$`, pdfTextBody(full.ForLLM), "complete fixture text premise")
		res := tool.Execute(context.Background(), map[string]any{"path": "report.pdf", "offset": 0, "length": 6})
		if res.IsError {
			t.Fatalf("paged PDF read errored: %s", clip(res.ForLLM))
		}
		// FR-8/F14: exactly six extracted characters, not raw bytes. The
		// reference full read defines the character domain (including any
		// extractor newline); the spec does not require trimming it.
		pdfAssertPagedWindow(t, "paged", full.ForLLM, res.ForLLM, "report.pdf", 2, 0, 6)
		shifted := tool.Execute(context.Background(), map[string]any{"path": "report.pdf", "offset": 5, "length": 5})
		require.False(t, shifted.IsError)
		pdfAssertPagedWindow(t, "shifted", full.ForLLM, shifted.ForLLM, "report.pdf", 2, 5, 5)
		// N7: eleven characters of fixture text must not fit in a six-
		// character window. Exact equality above also detects any suffix.
		if strings.Contains(res.ForLLM, "PAGE 1 text") {
			t.Fatalf("the six-character window leaked past char 5 ('PAGE 1 text' is 11 chars):\n%s", clip(res.ForLLM))
		}
		if strings.Contains(res.ForLLM, "PAGE 2") {
			t.Fatalf("paged window leaked beyond the requested characters:\n%s", clip(res.ForLLM))
		}
		if strings.Contains(res.ForLLM, "1 0 obj") {
			t.Fatalf("paged read returned raw PDF bytes:\n%s", clip(res.ForLLM))
		}
	})
}

// TestReadFile_PDFPersisted (T8, A) — FR-8's persisted-result table: over
// `max_media_size` with text → text + L1; without text → error naming the
// file (E1); > 25 MiB → today's error; with offset/length a no-text PDF is
// E3 and a not-parseable one is E2, each naming the file.
func TestReadFile_PDFPersisted(t *testing.T) {
	t.Run("over max_media_size with text: text plus L1", func(t *testing.T) {
		root := t.TempDir()
		// ~4.4 KB ASCII PDF with real text (padding lives in a content-stream
		// comment, so the document stays a valid text PDF).
		data := buildTextPDF(t, "1.4", 1, true, func(int) string {
			return "BT /F1 12 Tf 72 720 Td (compressed hello) Tj ET\n%" + strings.Repeat("p", 3000) + "\n"
		})
		writePDF(t, root, "big.pdf", data)

		tool := NewReadFileTool(root, false, MaxReadFileSize)
		tool.SetMaxInspectionImageBytes(2048)
		res := tool.Execute(context.Background(), map[string]any{"path": "big.pdf"})
		if res.IsError {
			t.Fatalf("over-limit PDF with extractable text must still show its text, got error: %s", clip(res.ForLLM))
		}
		if !strings.Contains(res.ForLLM, "compressed hello") {
			t.Fatalf("extracted text missing alongside L1:\n%s", clip(res.ForLLM))
		}
		// §5.6 L1, COMPLETE fixed content with the operator's setting VALUE:
		// "[PDF over the <max_media_size> media size setting; not sent
		// natively]" — the notice must carry the number the operator set
		// (2048) so the user can act on it.
		l1Idx := strings.Index(res.ForLLM, "[PDF over the ")
		if l1Idx < 0 {
			t.Fatalf("L1 notice ([PDF over the <setting> media size setting; not sent natively]) missing:\n%s", clip(res.ForLLM))
		}
		if !strings.Contains(res.ForLLM[l1Idx:], "2048") {
			t.Fatalf("L1 must carry the operator's media size setting value (2048):\n%s", clip(res.ForLLM))
		}
		if !strings.Contains(res.ForLLM[l1Idx:], "media size setting") || !strings.Contains(res.ForLLM[l1Idx:], pdfL1Sentence) {
			t.Fatalf("L1 shape incomplete (setting phrase + \"not sent natively\"):\n%s", clip(res.ForLLM))
		}
		// F14: L1 must sit within the SPA preview too.
		pdfAssertWithinPreviewLines(t, "over-limit-with-text", res.ForLLM, l1Idx)
	})

	t.Run("over max_media_size without text: error naming the file", func(t *testing.T) {
		root := t.TempDir()
		writePDF(t, root, "chart.pdf", padAfterEOF(t, buildScannedPDF(t, 1), 4096))

		tool := NewReadFileTool(root, false, MaxReadFileSize)
		tool.SetMaxInspectionImageBytes(2048)
		res := tool.Execute(context.Background(), map[string]any{"path": "chart.pdf"})
		// FR-8: no extractable text and over the media size → error E1 that
		// names the file (today: neither an error nor a named refusal).
		if !res.IsError {
			t.Fatalf("over-limit PDF without text must be an error (E1), got:\n%s", clip(res.ForLLM))
		}
		if !strings.Contains(res.ForLLM, "chart.pdf") {
			t.Fatalf("E1 must name the file, got:\n%s", clip(res.ForLLM))
		}
	})

	t.Run("over 25 MiB keeps today's too-large error", func(t *testing.T) {
		root := t.TempDir()
		// Compressed fixture (NULs in the first 512 bytes) padded past the
		// 25 MiB extraction cap after %%EOF.
		data := padAfterEOF(t, buildCompressedPDF(t, "tiny"), 26*1024*1024)
		writePDF(t, root, "huge.pdf", data)

		tool := NewReadFileTool(root, false, MaxReadFileSize)
		res := tool.Execute(context.Background(), map[string]any{"path": "huge.pdf"})
		if !res.IsError {
			t.Fatalf("a >25 MiB PDF must be refused, got:\n%s", clip(res.ForLLM))
		}
		if !strings.Contains(res.ForLLM, "document too large to extract") {
			t.Fatalf(">25 MiB must keep today's extraction-cap error, got:\n%s", clip(res.ForLLM))
		}
	})

	t.Run("pagination on a no-text pdf errors naming the file", func(t *testing.T) {
		root := t.TempDir()
		writePDF(t, root, "chart.pdf", buildScannedPDF(t, 1))

		tool := NewReadFileTool(root, false, MaxReadFileSize)
		res := tool.Execute(context.Background(), map[string]any{"path": "chart.pdf", "offset": 0, "length": 10})
		// FR-8: with offset/length, no text → error E3 naming the file.
		if !res.IsError {
			t.Fatalf("offset/length on a PDF without text must error (E3), got:\n%s", clip(res.ForLLM))
		}
		if !strings.Contains(res.ForLLM, "chart.pdf") {
			t.Fatalf("E3 must name the file, got:\n%s", clip(res.ForLLM))
		}
	})

	t.Run("pagination on a not-parseable pdf errors naming the file", func(t *testing.T) {
		root := t.TempDir()
		writePDF(t, root, "broken.pdf", buildBrokenPDF())

		tool := NewReadFileTool(root, false, MaxReadFileSize)
		res := tool.Execute(context.Background(), map[string]any{"path": "broken.pdf", "offset": 0, "length": 10})
		// FR-8: with offset/length, not parseable locally → error E2 naming
		// the file (today: an unnamed extraction failure).
		if !res.IsError {
			t.Fatalf("offset/length on a not-parseable PDF must error (E2), got:\n%s", clip(res.ForLLM))
		}
		if !strings.Contains(res.ForLLM, "broken.pdf") {
			t.Fatalf("E2 must name the file, got:\n%s", clip(res.ForLLM))
		}
	})
}
