package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeDocxBytes synthesizes a minimal valid .docx (OOXML zip) with the given
// paragraphs. It includes [Content_Types].xml so pkg/docextract's magic-byte
// sniff routes the file through the OOXML text extractor instead of the
// generic zip-manifest path (every real .docx carries this part).
func makeDocxBytes(t *testing.T, paragraphs ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	contentTypes := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
		`</Types>`
	ct, err := zw.Create("[Content_Types].xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ct.Write([]byte(contentTypes)); err != nil {
		t.Fatal(err)
	}

	var xmlBuf strings.Builder
	xmlBuf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	xmlBuf.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>`)
	for _, p := range paragraphs {
		xmlBuf.WriteString(`<w:p><w:r><w:t>` + p + `</w:t></w:r></w:p>`)
	}
	xmlBuf.WriteString(`</w:body></w:document>`)

	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(xmlBuf.String())); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makeXlsxBytes synthesizes a minimal valid .xlsx (OOXML zip) with one sheet of
// inline cells. It includes [Content_Types].xml so pkg/docextract's magic-byte
// sniff routes the file through the OOXML text extractor instead of the
// generic zip-manifest path (every real .xlsx carries this part).
func makeXlsxBytes(t *testing.T, rows [][]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	contentTypes := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
		`<Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>` +
		`</Types>`
	ct, err := zw.Create("[Content_Types].xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ct.Write([]byte(contentTypes)); err != nil {
		t.Fatal(err)
	}

	var sheetBuf strings.Builder
	sheetBuf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sheetBuf.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for ri, row := range rows {
		sheetBuf.WriteString(`<row r="`)
		sheetBuf.WriteByte(byte('1' + ri))
		sheetBuf.WriteString(`">`)
		for _, cell := range row {
			sheetBuf.WriteString(`<c t="inlineStr"><is><t>` + cell + `</t></is></c>`)
		}
		sheetBuf.WriteString(`</row>`)
	}
	sheetBuf.WriteString(`</sheetData></worksheet>`)

	w, err := zw.Create("xl/worksheets/sheet1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(sheetBuf.String())); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeWorkspaceFile writes data to <workspace>/<name> and returns the path.
func writeWorkspaceFile(t *testing.T, workspace, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(workspace, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestReadFile_ExtractsDocx verifies a Word document (an opaque zip binary) is
// decoded to plain text instead of being rejected as binary.
func TestReadFile_ExtractsDocx(t *testing.T) {
	ws := t.TempDir()
	path := writeWorkspaceFile(t, ws, "report.docx", makeDocxBytes(t, "Quarterly summary", "Revenue up 20 percent"))

	tool := NewReadFileTool(ws, false, MaxReadFileSize)
	res := tool.Execute(context.Background(), map[string]any{"path": path})

	if res.IsError {
		t.Fatalf("expected docx extraction to succeed, got error: %s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "Quarterly summary") || !strings.Contains(res.ForLLM, "Revenue up 20 percent") {
		t.Fatalf("expected extracted paragraphs in output, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "extracted text") {
		t.Fatalf("expected document header marker, got:\n%s", res.ForLLM)
	}
}

// TestReadFile_ExtractsXlsx verifies a spreadsheet is decoded to tab-separated
// rows of cell text.
func TestReadFile_ExtractsXlsx(t *testing.T) {
	ws := t.TempDir()
	path := writeWorkspaceFile(t, ws, "sheet.xlsx", makeXlsxBytes(t, [][]string{
		{"Name", "Score"},
		{"Ada", "99"},
	}))

	tool := NewReadFileTool(ws, false, MaxReadFileSize)
	res := tool.Execute(context.Background(), map[string]any{"path": path})

	if res.IsError {
		t.Fatalf("expected xlsx extraction to succeed, got error: %s", res.ForLLM)
	}
	for _, want := range []string{"Name", "Score", "Ada", "99"} {
		if !strings.Contains(res.ForLLM, want) {
			t.Fatalf("expected %q in extracted output, got:\n%s", want, res.ForLLM)
		}
	}
}

// TestReadFile_OpaqueBinaryStillRejected verifies non-document binaries (no
// extractable type) are still rejected rather than silently extracted.
func TestReadFile_OpaqueBinaryStillRejected(t *testing.T) {
	ws := t.TempDir()
	// A blob with null bytes and no document extension.
	path := writeWorkspaceFile(t, ws, "blob.bin", []byte{0x00, 0x01, 0x02, 0x00, 0xff, 0xfe})

	tool := NewReadFileTool(ws, false, MaxReadFileSize)
	res := tool.Execute(context.Background(), map[string]any{"path": path})

	if !res.IsError {
		t.Fatalf("expected opaque binary to be rejected, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "binary file detected") {
		t.Fatalf("expected binary rejection message, got: %s", res.ForLLM)
	}
}

// TestReadFile_CorruptDocumentGivesClearError verifies a file with a document
// extension but invalid contents yields an extraction error (not a misleading
// generic binary rejection).
func TestReadFile_CorruptDocumentGivesClearError(t *testing.T) {
	ws := t.TempDir()
	// Document extension, contains a null byte (so it reaches the binary branch),
	// but is not a valid zip archive.
	path := writeWorkspaceFile(t, ws, "broken.docx", []byte("PK\x03\x04not a real zip\x00garbage"))

	tool := NewReadFileTool(ws, false, MaxReadFileSize)
	res := tool.Execute(context.Background(), map[string]any{"path": path})

	if !res.IsError {
		t.Fatalf("expected corrupt docx to error, got:\n%s", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "could not extract document text") {
		t.Fatalf("expected extraction-failure message, got: %s", res.ForLLM)
	}
}

// TestReadFile_DocumentPagination verifies offset/length page through extracted
// text by character position and advertise a resumable offset.
func TestReadFile_DocumentPagination(t *testing.T) {
	ws := t.TempDir()
	// Two paragraphs joined by a newline produce deterministic extracted text.
	path := writeWorkspaceFile(t, ws, "long.docx", makeDocxBytes(t, "AAAAAAAAAA", "BBBBBBBBBB"))

	tool := NewReadFileTool(ws, false, MaxReadFileSize)

	// First page: 5 chars from the start -> should be truncated with a resume offset.
	first := tool.Execute(context.Background(), map[string]any{
		"path": path, "offset": float64(0), "length": float64(5),
	})
	if first.IsError {
		t.Fatalf("page 1 errored: %s", first.ForLLM)
	}
	if !strings.Contains(first.ForLLM, "TRUNCATED") || !strings.Contains(first.ForLLM, "offset=5") {
		t.Fatalf("expected truncation with offset=5, got:\n%s", first.ForLLM)
	}
	if !strings.Contains(first.ForLLM, "AAAAA") {
		t.Fatalf("expected first 5 chars 'AAAAA', got:\n%s", first.ForLLM)
	}

	// Page from a large offset past the end -> end-of-document, no content.
	beyond := tool.Execute(context.Background(), map[string]any{
		"path": path, "offset": float64(100000), "length": float64(10),
	})
	if beyond.IsError {
		t.Fatalf("beyond-EOF page errored: %s", beyond.ForLLM)
	}
	if !strings.Contains(beyond.ForLLM, "END OF DOCUMENT") {
		t.Fatalf("expected end-of-document marker, got:\n%s", beyond.ForLLM)
	}
}

// TestReadFile_DocumentSymlinkEscapeBlocked verifies extraction reads through
// the sandboxed filesystem: a document symlink pointing outside the workspace
// is blocked, not extracted.
func TestReadFile_DocumentSymlinkEscapeBlocked(t *testing.T) {
	root := t.TempDir()
	ws := filepath.Join(root, "workspace")
	if err := os.MkdirAll(ws, 0o755); err != nil {
		t.Fatal(err)
	}

	// A real document living OUTSIDE the workspace.
	secret := filepath.Join(root, "secret.docx")
	if err := os.WriteFile(secret, makeDocxBytes(t, "TOP SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(ws, "leak.docx")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlink not supported: %v", err)
	}

	tool := NewReadFileTool(ws, true, MaxReadFileSize) // restrict=true -> os.Root sandbox
	res := tool.Execute(context.Background(), map[string]any{"path": link})

	if !res.IsError {
		t.Fatalf("expected sandbox to block symlink-escape document, got:\n%s", res.ForLLM)
	}
	if strings.Contains(res.ForLLM, "TOP SECRET") {
		t.Fatalf("sandbox escape: leaked out-of-workspace document content:\n%s", res.ForLLM)
	}
}
