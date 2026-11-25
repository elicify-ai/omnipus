package tools

// In-test PDF fixtures for spec #1968 (read-file native PDF). Nothing here
// is committed to disk or embedded (§6.1): every fixture is assembled in
// memory from numbered objects with a byte-accurate xref table, validated
// against today's extractor before use (TestPDFFixtureBuilders_Parse — the
// builder is the oracle for the tool-level tests, so it must itself be
// proven: object-id collisions, a broken trailing window or a wrong /Root
// fail here first instead of masquerading as behavioural failures).

import (
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/docextract"
	"github.com/stretchr/testify/require"
)

// This existing-symbol helper is deliberately outside the B file: its
// roots and independent text/media settings can be verified on base (F2/N11).
func pdfReaders(t *testing.T, root, name string, data []byte, maxSize int) []struct {
	name   string
	exec   func(context.Context, map[string]any) *ToolResult
	reader *ReadFileTool
} {
	t.Helper()
	writePDF(t, root, name, data)
	libDir := filepath.Join(root, ".library")
	require.NoError(t, os.MkdirAll(libDir, 0o700))
	writePDF(t, libDir, name, data)
	read := NewReadFileTool(root, false, MaxReadFileSize)
	library := NewLibraryReadTool(root, false, MaxReadFileSize)
	read.SetMaxInspectionImageBytes(maxSize)
	library.SetMaxInspectionImageBytes(maxSize)
	return []struct {
		name   string
		exec   func(context.Context, map[string]any) *ToolResult
		reader *ReadFileTool
	}{
		{"read_file", read.Execute, read},
		{"library_read", library.Execute, library.inner},
	}
}

func TestPDF1968ReaderHarness(t *testing.T) {
	root := t.TempDir()
	data := buildTextPDF(t, "1.4", 1, true, nil)
	for _, reader := range pdfReaders(t, root, "report.pdf", data, 16) {
		require.Equal(t, int64(16), reader.reader.maxInspectionImageBytes, "%s: media cap, not text cap", reader.name)
		require.Equal(t, int64(MaxReadFileSize), reader.reader.maxSize, "%s: normal text cap stays intact", reader.name)
	}
	for _, path := range []string{filepath.Join(root, "report.pdf"), filepath.Join(root, ".library", "report.pdf")} {
		got, err := os.ReadFile(path)
		require.NoError(t, err)
		require.Equal(t, data, got, "fixture is byte-exact under each configured root")
	}
}

// pdfObj is one numbered object body; assemblePDF emits the complete file
// with a byte-accurate xref table (one entry per object number 0..max, free
// entries in the gaps — a real xref layout).
type pdfObj struct {
	num    int
	body   string
	offset int
}

const fixtureCatalogNum = 888
const fixturePagesNum = 999

// assemblePDF lays out header + objects + xref/trailer/startxref and returns
// the complete PDF bytes. trailerExtra is appended to the trailer dictionary
// (e.g. /Encrypt + /ID). highest is derived from the objects actually
// present, and /Root is located by scanning for the /Type /Catalog object,
// so both the derived-numbering builder and the fixed-numbering single-page
// builders stay collision-free and correctly rooted (F3).
func assemblePDF(header string, objs []pdfObj, trailerExtra string) []byte {
	highest := 0
	byNum := make(map[int]*pdfObj, len(objs))
	rootNum := 0
	for i := range objs {
		byNum[objs[i].num] = &objs[i]
		if objs[i].num > highest {
			highest = objs[i].num
		}
		if strings.Contains(objs[i].body, "/Type /Catalog") {
			rootNum = objs[i].num
		}
	}
	if rootNum == 0 {
		rootNum = fixtureCatalogNum
	}

	var buf bytes.Buffer
	offset := 0
	write := func(s string) {
		offset += len(s)
		buf.WriteString(s)
	}
	write(header + "\n")
	for i := range objs {
		objs[i].offset = offset
		write(fmt.Sprintf("%d 0 obj\n%s\nendobj\n", objs[i].num, objs[i].body))
	}
	xrefStart := offset
	write(fmt.Sprintf("xref\n0 %d\n", highest+1))
	write("0000000000 65535 f \n")
	for n := 1; n <= highest; n++ {
		if o, ok := byNum[n]; ok {
			write(fmt.Sprintf("%010d 00000 n \n", o.offset))
		} else {
			write("0000000000 65535 f \n")
		}
	}
	trailer := fmt.Sprintf("/Size %d /Root %d 0 R", highest+1, rootNum)
	if trailerExtra != "" {
		trailer += " " + trailerExtra
	}
	write(fmt.Sprintf("trailer\n<< %s >>\nstartxref\n%d\n%%%%EOF\n", trailer, xrefStart))
	return buf.Bytes()
}

// fixtureLayout derives every object number from the page count so no two
// objects share a number at any N (F3): font=1, contents=2..N+1,
// pages=N+2..2N+1, tree=2N+2, catalog=2N+3.
type fixtureLayout struct {
	font     int
	contents []int
	pages    []int
	tree     int
	catalog  int
}

func fixtureLayoutFor(pages int) fixtureLayout {
	l := fixtureLayout{
		font:     1,
		contents: make([]int, pages),
		pages:    make([]int, pages),
		tree:     2*pages + 2,
		catalog:  2*pages + 3,
	}
	for i := 0; i < pages; i++ {
		l.contents[i] = 2 + i
		l.pages[i] = pages + 2 + i
	}
	return l
}

// buildTextPDF builds a valid N-page PDF whose page i draws "PAGE i text" as
// ASCII text operators (uncompressed content stream). version is the header
// version string (e.g. "1.4", "2.0"); withCount controls whether the pages
// tree carries a /Count entry. contentFor may override the per-page content
// stream (its /Length is computed from what it returns).
func buildTextPDF(t *testing.T, version string, pages int, withCount bool, contentFor func(page int) string) []byte {
	t.Helper()
	if contentFor == nil {
		contentFor = func(page int) string {
			return fmt.Sprintf("BT /F1 12 Tf 72 720 Td (PAGE %d text) Tj ET", page+1)
		}
	}
	l := fixtureLayoutFor(pages)

	// Font shared by all pages.
	objs := []pdfObj{{num: l.font, body: `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`}}

	// Content streams, one per page.
	for i := 0; i < pages; i++ {
		objs = append(objs, pdfObj{num: l.contents[i], body: fmt.Sprintf(`<< /Length %d >>`+"\n"+
			"stream\n%s\nendstream", len(contentFor(i)), contentFor(i))})
	}

	// Page objects.
	for i := 0; i < pages; i++ {
		objs = append(objs, pdfObj{num: l.pages[i], body: fmt.Sprintf(
			`<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 %d 0 R >> >> /Contents %d 0 R >>`,
			l.tree, l.font, l.contents[i])})
	}

	// Pages tree.
	countDict := ""
	if withCount {
		countDict = fmt.Sprintf("/Count %d ", pages)
	}
	kids := ""
	for i, id := range l.pages {
		if i > 0 {
			kids += " "
		}
		kids += fmt.Sprintf("%d 0 R", id)
	}
	objs = append(objs, pdfObj{num: l.tree, body: fmt.Sprintf(
		`<< %s/Type /Pages /Kids [%s] >>`, countDict, kids)})
	objs = append(objs, pdfObj{num: l.catalog,
		body: fmt.Sprintf(`<< /Type /Catalog /Pages %d 0 R >>`, l.tree)})

	return assemblePDF(fmt.Sprintf("%%PDF-%s", version), objs, "")
}

// buildScannedPDF builds a valid N-page PDF whose pages draw nothing: a real
// document with no extractable text (the scanned-document shape, without
// needing image XObjects).
func buildScannedPDF(t *testing.T, pages int) []byte {
	t.Helper()
	return buildTextPDF(t, "1.4", pages, true, func(int) string { return "" })
}

// buildEncryptedPDF builds a structurally valid PDF carrying an /Encrypt
// dictionary (standard security handler, empty user password, V1 R2 40-bit —
// the keys are fixture filler; the reader must detect the ENCRYPTION, not
// decrypt). Single page: the fixed 888/999 numbering cannot collide here.
func buildEncryptedPDF(t *testing.T) []byte {
	t.Helper()
	objs := []pdfObj{
		{num: 1, body: `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`},
		{num: 2, body: "<< /Length 44 >>\nstream\nBT /F1 12 Tf 72 720 Td (secret) Tj ET\nendstream"},
		{num: 3, body: fmt.Sprintf(
			`<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 1 0 R >> >> /Contents 2 0 R >>`,
			fixturePagesNum)},
		{num: fixturePagesNum, body: `<< /Count 1 /Type /Pages /Kids [3 0 R] >>`},
		// The encryption dictionary: standard security handler, R2/V1.
		{num: 998, body: `<< /Filter /Standard /V 1 /R 2 /O (01234567890123456789012345678901) /U (01234567890123456789012345678901) /P -1 >>`},
		{num: fixtureCatalogNum, body: fmt.Sprintf(`<< /Type /Catalog /Pages %d 0 R >>`, fixturePagesNum)},
	}
	return assemblePDF("%PDF-1.4", objs,
		`/Encrypt 998 0 R /ID [<0123456789ABCDEF0123456789ABCDEF> <0123456789ABCDEF0123456789ABCDEF>]`)
}

// buildCompressedPDF builds a one-page PDF whose content stream is
// zlib-compressed (FlateDecode) — the realistic real-world shape, and the
// one whose raw bytes carry NULs in the first 512 bytes. Single page: the
// fixed 888/999 numbering cannot collide here.
func buildCompressedPDF(t *testing.T, text string) []byte {
	t.Helper()
	var z bytes.Buffer
	w := zlib.NewWriter(&z)
	fmt.Fprintf(w, "BT /F1 12 Tf 72 720 Td (%s) Tj ET", text)
	if err := w.Close(); err != nil {
		t.Fatalf("compress content: %v", err)
	}
	content := z.Bytes()
	objs := []pdfObj{
		{num: 1, body: `<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica >>`},
		{num: 2, body: fmt.Sprintf(`<< /Length %d /Filter /FlateDecode >>`+"\nstream\n%s\nendstream", len(content), content)},
		{num: 3, body: fmt.Sprintf(
			`<< /Type /Page /Parent %d 0 R /MediaBox [0 0 612 792] /Resources << /Font << /F1 1 0 R >> >> /Contents 2 0 R >>`,
			fixturePagesNum)},
		{num: fixturePagesNum, body: `<< /Count 1 /Type /Pages /Kids [3 0 R] >>`},
		{num: fixtureCatalogNum, body: fmt.Sprintf(`<< /Type /Catalog /Pages %d 0 R >>`, fixturePagesNum)},
	}
	return assemblePDF("%PDF-1.4", objs, "")
}

// buildBrokenPDF builds bytes that start with the %PDF- magic but are
// structurally broken, and carry NUL bytes early (so today's NUL check
// routes them into the document-extraction path rather than the raw read).
func buildBrokenPDF() []byte {
	return []byte("%PDF-1.4\n\x00\x00this document is broken from the third byte on, no xref, no trailer, no objects")
}

// padAfterEOF grows a PDF to at least minSize bytes by appending ASCII
// comment padding after the final %%EOF followed by a complete startxref
// line repeating the REAL xref offset and a final %%EOF (F3: bare comment
// lines break the reader's last-100-byte trailer window — the tail keeps a
// complete `startxref` line and an %%EOF inside it, so the padded document
// stays parseable).
func padAfterEOF(t *testing.T, data []byte, minSize int) []byte {
	t.Helper()
	if len(data) >= minSize {
		return data
	}
	raw := string(data)
	idx := strings.LastIndex(raw, "\nstartxref\n")
	if idx < 0 {
		t.Fatalf("fixture broken: no startxref line to re-emit")
	}
	rest := raw[idx+len("\nstartxref\n"):]
	if nl := strings.IndexByte(rest, '\n'); nl >= 0 {
		rest = rest[:nl]
	}
	off := strings.TrimSpace(rest)
	if _, err := strconv.Atoi(off); err != nil {
		t.Fatalf("fixture broken: startxref value %q is not a number", off)
	}
	tail := "\nstartxref\n" + off + "\n%%EOF\n"
	pad := minSize - len(data) - len(tail)
	if pad < 0 {
		pad = 0
	}
	out := append([]byte(nil), data...)
	out = append(out, bytes.Repeat([]byte("% padding padding padding\n"), pad/26+2)...)
	out = append(out, tail...)
	return out
}

// pdfFixtureGuardTextFree asserts data is a structurally valid PDF whose
// extraction is text-free. Today's extractor reports a parsed but text-free
// document as ok=false with the "no extractable text" reason (the failure is
// returned only after pdf open and the plain-text walk succeeded); either
// that or a clean empty-text outcome proves the fixture valid — an
// open/extraction/read failure means the fixture itself is broken.
func pdfFixtureGuardTextFree(t *testing.T, label string, data []byte, pdfMIME string) {
	t.Helper()
	text, ok, reason := docextract.ExtractBytes(data, pdfMIME, label+".pdf")
	switch {
	case ok:
		if strings.TrimSpace(text) != "" {
			t.Fatalf("%s must have no extractable text, got %q", label, text)
		}
	case strings.Contains(reason, "no extractable text"):
		// parsed and text-free — today's outcome for a text-free document
	case strings.Contains(reason, "pdf open failed"),
		strings.Contains(reason, "pdf text extraction failed"),
		strings.Contains(reason, "pdf read failed"):
		t.Fatalf("%s is structurally invalid: %s", label, reason)
	default:
		t.Fatalf("%s failed extraction unexpectedly: %s", label, reason)
	}
}

// TestPDFFixtureBuilders_Parse (G, F3) — validate the builders themselves
// through the production extractor before the tool-level tests rely on
// them: exact sizes, page counts, unknown pages, collision-free numbering,
// and padding that keeps the document parseable.
func TestPDFFixtureBuilders_Parse(t *testing.T) {
	pdfMIME := "application/pdf"

	t.Run("text pdf extracts its page text", func(t *testing.T) {
		for _, pages := range []int{1, 2, 7, 700} {
			data := buildTextPDF(t, "1.4", pages, true, nil)
			text, ok, reason := docextract.ExtractBytes(data, pdfMIME, "t.pdf")
			if !ok {
				t.Fatalf("%d-page text fixture must extract (reason %q)", pages, reason)
			}
			if !strings.Contains(text, "PAGE 1 text") ||
				(pages > 1 && !strings.Contains(text, fmt.Sprintf("PAGE %d text", pages))) {
				t.Fatalf("%d-page text fixture text incomplete: %q", pages, text)
			}
		}
	})

	t.Run("scanned pdf extracts nothing", func(t *testing.T) {
		for _, pages := range []int{1, 700} {
			pdfFixtureGuardTextFree(t, fmt.Sprintf("%d-page scanned", pages), buildScannedPDF(t, pages), pdfMIME)
		}
	})

	t.Run("no /Count means the page count is unstated", func(t *testing.T) {
		data := buildTextPDF(t, "1.4", 3, false, nil)
		if strings.Contains(string(data), "/Count") {
			t.Fatal("withCount=false fixture must not carry a /Count entry anywhere")
		}
		pdfFixtureGuardTextFree(t, "no-count", data, pdfMIME)
	})

	t.Run("object numbers never collide", func(t *testing.T) {
		re := regexp.MustCompile(`(?m)^(\d+) 0 obj$`)
		for _, pages := range []int{1, 700} {
			seen := map[int]bool{}
			for _, m := range re.FindAllStringSubmatch(string(buildTextPDF(t, "1.4", pages, true, nil)), -1) {
				n, err := strconv.Atoi(m[1])
				if err != nil {
					t.Fatalf("bad object number %q", m[1])
				}
				if seen[n] {
					t.Fatalf("%d-page fixture reuses object number %d", pages, n)
				}
				seen[n] = true
			}
			if len(seen) != 2*pages+3 {
				t.Fatalf("%d-page fixture has %d objects, want %d (font + N contents + N pages + tree + catalog)", pages, len(seen), 2*pages+3)
			}
		}
	})

	t.Run("encrypted and compressed fixtures parse as intended", func(t *testing.T) {
		if _, ok, reason := docextract.ExtractBytes(buildEncryptedPDF(t), pdfMIME, "e.pdf"); ok {
			t.Fatalf("encrypted fixture must NOT extract (reader must see the encryption), reason %q", reason)
		}
		text, ok, reason := docextract.ExtractBytes(buildCompressedPDF(t, "COMPRESSED TEXT MARKER"), pdfMIME, "c.pdf")
		if !ok || !strings.Contains(text, "COMPRESSED TEXT MARKER") {
			t.Fatalf("compressed fixture must extract its text (ok=%v reason=%q text=%q)", ok, reason, text)
		}
	})

	t.Run("padding keeps the document parseable", func(t *testing.T) {
		for _, minSize := range []int{1024, 4096, 8192} {
			data := padAfterEOF(t, buildTextPDF(t, "1.4", 1, true, nil), minSize)
			if len(data) < minSize {
				t.Fatalf("padAfterEOF(min %d) produced %d bytes", minSize, len(data))
			}
			text, ok, reason := docextract.ExtractBytes(data, pdfMIME, "t.pdf")
			if !ok || !strings.Contains(text, "PAGE 1 text") {
				t.Fatalf("padded fixture must stay a parseable text PDF (ok=%v reason=%q text=%q)", ok, reason, text)
			}
		}
	})
}
