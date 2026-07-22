// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package docextract

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeDocx synthesizes a minimal .docx zip in memory with the given paragraph texts.
// Includes [Content_Types].xml so the OOXML magic-byte sniff in pkg/docextract/extract.go
// routes the file through the OOXML extractor instead of the generic archive path.
func makeDocx(t *testing.T, paragraphs ...string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	// [Content_Types].xml — required for the OOXML magic-byte sniff.
	if w, err := zw.Create("[Content_Types].xml"); err != nil {
		t.Fatal(err)
	} else {
		ct := `<?xml version="1.0" encoding="UTF-8"?>` +
			`<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">` +
			`<Default Extension="xml" ContentType="application/xml"/>` +
			`<Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>` +
			`</Types>`
		if _, err := w.Write([]byte(ct)); err != nil {
			t.Fatal(err)
		}
	}

	// Build word/document.xml
	var xmlBuf strings.Builder
	xmlBuf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	xmlBuf.WriteString(`<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">`)
	xmlBuf.WriteString(`<w:body>`)
	for _, p := range paragraphs {
		xmlBuf.WriteString(`<w:p><w:r><w:t>`)
		xmlBuf.WriteString(p)
		xmlBuf.WriteString(`</w:t></w:r></w:p>`)
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

// makePptx synthesizes a minimal .pptx zip with one slide containing the given text.
func makePptx(t *testing.T, slideText string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	slideXML := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main" xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">` +
		`<p:cSld><p:spTree><p:sp><p:txBody>` +
		`<a:p><a:r><a:t>` + slideText + `</a:t></a:r></a:p>` +
		`</p:txBody></p:sp></p:spTree></p:cSld></p:sld>`

	w, err := zw.Create("ppt/slides/slide1.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte(slideXML)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makeXlsx synthesizes a minimal .xlsx zip with one sheet and inline cell values.
func makeXlsx(t *testing.T, rows [][]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	var sheetBuf strings.Builder
	sheetBuf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>`)
	sheetBuf.WriteString(`<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData>`)
	for ri, row := range rows {
		sheetBuf.WriteString(`<row r="`)
		sheetBuf.WriteByte(byte('1' + ri))
		sheetBuf.WriteString(`">`)
		for _, cell := range row {
			// Inline string cell (type t="inlineStr" or just use s in v for simplicity)
			sheetBuf.WriteString(`<c t="inlineStr"><is><t>`)
			sheetBuf.WriteString(cell)
			sheetBuf.WriteString(`</t></is></c>`)
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

func TestExtract_PlainText(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello world\nline two"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, ok, reason := Extract(path, "text/plain", "hello.txt")
	if !ok {
		t.Fatalf("expected ok=true, reason=%q", reason)
	}
	if !strings.Contains(text, "hello world") {
		t.Fatalf("expected 'hello world' in text, got %q", text)
	}
}

func TestExtract_CSV(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.csv")
	if err := os.WriteFile(path, []byte("a,b,c\n1,2,3"), 0o644); err != nil {
		t.Fatal(err)
	}
	text, ok, reason := Extract(path, "text/csv", "data.csv")
	if !ok {
		t.Fatalf("expected ok=true, reason=%q", reason)
	}
	if !strings.Contains(text, "a,b,c") {
		t.Fatalf("expected csv header in text, got %q", text)
	}
}

func TestExtract_Docx(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "document.docx")
	data := makeDocx(t, "Hello from docx", "Second paragraph")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	const docxMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	text, ok, reason := Extract(path, docxMIME, "document.docx")
	if !ok {
		t.Fatalf("expected ok=true, reason=%q", reason)
	}
	if !strings.Contains(text, "Hello from docx") {
		t.Fatalf("expected docx text, got %q", text)
	}
	if !strings.Contains(text, "Second paragraph") {
		t.Fatalf("expected second paragraph, got %q", text)
	}
}

func TestExtract_DocxMIMEFallback(t *testing.T) {
	// No MIME type given — detect from extension only.
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.docx")
	data := makeDocx(t, "Extension-only detection")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	text, ok, reason := Extract(path, "", "doc.docx")
	if !ok {
		t.Fatalf("expected ok=true, reason=%q", reason)
	}
	if !strings.Contains(text, "Extension-only detection") {
		t.Fatalf("expected text, got %q", text)
	}
}

func TestExtract_Pptx(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "slides.pptx")
	data := makePptx(t, "Slide one content")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	const pptxMIME = "application/vnd.openxmlformats-officedocument.presentationml.presentation"
	text, ok, reason := Extract(path, pptxMIME, "slides.pptx")
	if !ok {
		t.Fatalf("expected ok=true, reason=%q", reason)
	}
	if !strings.Contains(text, "Slide one content") {
		t.Fatalf("expected slide text, got %q", text)
	}
}

func TestExtract_Xlsx(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sheet.xlsx")
	data := makeXlsx(t, [][]string{{"Name", "Age"}, {"Alice", "30"}})
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	text, ok, reason := Extract(path, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet", "sheet.xlsx")
	if !ok {
		t.Fatalf("expected ok=true, reason=%q", reason)
	}
	if !strings.Contains(text, "Name") {
		t.Fatalf("expected cell content, got %q", text)
	}
}

func TestExtract_UnknownBinary_ReturnsOKFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "data.bin")
	// Write some random binary bytes that are not valid UTF-8
	if err := os.WriteFile(path, []byte{0xFF, 0xFE, 0x00, 0x01, 0x02, 0x80, 0x81, 0x82}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, ok, reason := Extract(path, "application/octet-stream", "data.bin")
	if ok {
		t.Fatal("expected ok=false for unknown binary")
	}
	if reason == "" {
		t.Fatal("expected non-empty reason for ok=false")
	}
}

func TestExtract_InvalidDocxFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.docx")
	if err := os.WriteFile(path, []byte("not a zip file"), 0o644); err != nil {
		t.Fatal(err)
	}
	const badDocxMIME = "application/vnd.openxmlformats-officedocument.wordprocessingml.document"
	_, ok, reason := Extract(path, badDocxMIME, "bad.docx")
	if ok {
		t.Fatal("expected ok=false for invalid docx")
	}
	if reason == "" {
		t.Fatal("expected non-empty reason")
	}
}

func TestExtract_PDFGracefulFallback(t *testing.T) {
	// A minimal valid-ish PDF that may or may not produce text.
	// The important invariant: Extract never panics on any PDF input,
	// and ok=false comes with a non-empty reason.
	dir := t.TempDir()
	path := filepath.Join(dir, "minimal.pdf")
	// This is a truncated/invalid PDF — text extraction should fail gracefully.
	if err := os.WriteFile(path, []byte("%PDF-1.4\n%%EOF"), 0o644); err != nil {
		t.Fatal(err)
	}
	// ok=true (empty text) or ok=false is both acceptable; what's not acceptable is a panic.
	text, ok, reason := Extract(path, "application/pdf", "minimal.pdf")
	if !ok && reason == "" {
		t.Fatal("ok=false requires a non-empty reason")
	}
	_ = text
}

func TestExtract_Truncation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.txt")
	// Write more than maxExtractRunes characters.
	big := strings.Repeat("x", maxExtractRunes+1000)
	if err := os.WriteFile(path, []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	text, ok, reason := Extract(path, "text/plain", "big.txt")
	if !ok {
		t.Fatalf("expected ok=true, reason=%q", reason)
	}
	if !strings.HasSuffix(text, "…[truncated]") {
		t.Fatalf("expected truncation marker, got suffix %q", text[len(text)-20:])
	}
	runes := []rune(text)
	if len(runes) > maxExtractRunes+20 { // some slack for the truncation marker
		t.Fatalf("text exceeds max rune limit: %d runes", len(runes))
	}
}

func TestExtract_EmptyFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(path, []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}
	_, ok, reason := Extract(path, "text/plain", "empty.txt")
	if ok {
		t.Fatal("expected ok=false for empty file")
	}
	if reason == "" {
		t.Fatal("expected non-empty reason")
	}
}

func TestExtract_NonExistentFile(t *testing.T) {
	_, ok, reason := Extract("/nonexistent/path/file.txt", "text/plain", "file.txt")
	if ok {
		t.Fatal("expected ok=false for non-existent file")
	}
	if reason == "" {
		t.Fatal("expected non-empty reason")
	}
}

func TestExtract_SVG(t *testing.T) {
	// ADR-051 RD1 extension (Option B): SVG is XML text — extraction must
	// return the markup so a model can reason about the image from its
	// source when no raster path is available.
	dir := t.TempDir()
	path := filepath.Join(dir, "circle.svg")
	svg := `<svg xmlns="http://www.w3.org/2000/svg" width="100" height="100"><circle cx="50" cy="50" r="40" fill="blue"/></svg>`
	if err := os.WriteFile(path, []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	text, ok, reason := Extract(path, "image/svg+xml", "circle.svg")
	if !ok {
		t.Fatalf("expected ok=true for SVG, reason=%q", reason)
	}
	if !strings.Contains(text, `<circle cx="50"`) {
		t.Fatalf("expected SVG markup in text, got %q", text)
	}
}

func TestExtract_SVGByExtensionOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "icon.svg")
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`
	if err := os.WriteFile(path, []byte(svg), 0o644); err != nil {
		t.Fatal(err)
	}
	text, ok, reason := Extract(path, "", "icon.svg")
	if !ok {
		t.Fatalf("expected ok=true for .svg extension, reason=%q", reason)
	}
	if !strings.Contains(text, "<rect") {
		t.Fatalf("expected SVG markup in text, got %q", text)
	}
}
