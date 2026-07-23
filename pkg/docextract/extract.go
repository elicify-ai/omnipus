// Omnipus - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

// Package docextract extracts plain text from uploaded document files so
// the agent loop can inject their content into the conversation context.
// It is pure Go (CGO_ENABLED=0 compatible) and has no external runtime deps.
package docextract

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// maxExtractRunes is the maximum number of Unicode code points returned.
// Extraction is stopped after this limit and a truncation notice is appended.
const (
	maxExtractRunes      = 100_000
	maxArchiveEntries    = 1000
	maxArchiveNameLen    = 256
	maxDecompressedBytes = 25 * 1024 * 1024
	maxManifestBytes     = 64 * 1024
)

// MaxDocBytes bounds how many raw bytes a caller should read into memory before
// handing them to ExtractBytes. It caps the cost of in-memory extraction (zip
// archives and PDFs must be fully buffered) so a hostile or oversized document
// cannot exhaust memory.
const MaxDocBytes = 25 << 20 // 25 MiB

// Extract attempts to read the plain-text content of the file at path.
//
//   - mime is the MIME type (may be empty).
//   - filename is used as a fallback to determine the format from the extension.
//
// Returns:
//   - text — extracted plain text, possibly truncated.
//   - ok — true when text extraction succeeded and produced non-empty output.
//   - reason — a short human-readable note for ok=false cases, empty when ok=true.
func Extract(path string, mime string, filename string) (text string, ok bool, reason string) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, fmt.Sprintf("open failed: %v", err)
	}
	defer f.Close()

	info, err := f.Stat()
	if err != nil {
		return "", false, fmt.Sprintf("stat failed: %v", err)
	}
	if info.Size() > MaxDocBytes {
		return "", false, fmt.Sprintf("document too large to extract (limit %d bytes)", MaxDocBytes)
	}

	data, err := io.ReadAll(io.LimitReader(f, MaxDocBytes+1))
	if err != nil {
		return "", false, fmt.Sprintf("read failed: %v", err)
	}
	if int64(len(data)) > MaxDocBytes {
		return "", false, fmt.Sprintf("document too large to extract (limit %d bytes)", MaxDocBytes)
	}
	return ExtractBytes(data, mime, filename)
}

// ExtractBytes is the in-memory counterpart of Extract: it extracts plain text
// from a document already buffered in data, opening no files of its own. This
// lets sandboxed callers (e.g. the read_file tool) feed bytes they read through
// their own confined filesystem, so extraction can never escape the sandbox by
// re-opening a raw path.
//
//   - mime is the MIME type (may be empty).
//   - filename is used as a fallback to determine the format from the extension.
func ExtractBytes(data []byte, mime string, filename string) (text string, ok bool, reason string) {
	if len(data) > MaxDocBytes {
		return "", false, fmt.Sprintf("document too large to extract (limit %d bytes)", MaxDocBytes)
	}

	ext := strings.ToLower(filepath.Ext(filename))
	lowerMIME := strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))

	// An explicit OOXML MIME selects its extractor directly.
	if isOOXML(lowerMIME, "") {
		return extractOOXMLBytes(data, ooxmlExtensionForMIME(lowerMIME, ext))
	}

	// OOXML is a ZIP container, so its package declaration takes precedence over
	// a generic or incorrect MIME and over ZIP suffix dispatch.
	if detectedExt, detected := detectOOXML(data); detected {
		return extractOOXMLBytes(data, detectedExt)
	}

	// Other recognized MIME values are authoritative over filename extensions.
	switch {
	case isPDF(lowerMIME, ""):
		return extractPDF(bytes.NewReader(data), int64(len(data)))
	case archiveKindForMIME(lowerMIME) != "":
		return extractArchive(data, lowerMIME, filename)
	case isTextLike(lowerMIME, ""):
		return extractTextBytes(data)
	}

	// With no recognized MIME, byte signatures keep renamed archives and PDFs
	// consistent with IsExtractableBytes.
	switch archiveKindForMagic(data) {
	case "zip":
		return extractArchive(data, "application/zip", filename)
	case "gzip":
		return extractArchive(data, "application/gzip", filename)
	case "tar":
		return extractArchive(data, "application/x-tar", filename)
	case "pdf":
		return extractPDF(bytes.NewReader(data), int64(len(data)))
	}

	switch {
	case isOOXML("", ext):
		return extractOOXMLBytes(data, ext)
	case archiveKindForFilename(filename) != "":
		return extractArchive(data, "", filename)
	case isPDF("", ext):
		return extractPDF(bytes.NewReader(data), int64(len(data)))
	case isTextLike("", ext):
		return extractTextBytes(data)
	default:
		return "", false, fmt.Sprintf("unsupported format (mime=%s, ext=%s)", mime, ext)
	}
}

// IsExtractable reports whether Extract / ExtractBytes can produce text for the
// given MIME type or filename extension. Callers use it to decide whether an
// otherwise-opaque binary is in fact a readable document.
func IsExtractable(mime string, filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	lowerMIME := strings.ToLower(strings.TrimSpace(strings.Split(mime, ";")[0]))
	return isTextLike(lowerMIME, ext) || isOOXML(lowerMIME, ext) || isPDF(lowerMIME, ext) ||
		archiveKindForMIME(lowerMIME) != "" || archiveKindForFilename(filename) != ""
}

func archiveKindForMagic(data []byte) string {
	switch {
	case len(data) >= 4 && bytes.Equal(data[:4], []byte("PK\x03\x04")):
		return "zip"
	case len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b:
		return "gzip"
	case len(data) >= 262 && string(data[257:262]) == "ustar":
		return "tar"
	case len(data) >= 5 && bytes.Equal(data[:5], []byte("%PDF-")):
		return "pdf"
	default:
		return ""
	}
}

// IsExtractableBytes reports whether bounded file bytes or the filename identify
// a format that ExtractBytes can safely decode.
func IsExtractableBytes(data []byte, filename string) bool {
	if _, ok := detectOOXML(data); ok {
		return true
	}
	return archiveKindForMagic(data) != "" || IsExtractable("", filename)
}

// isTextLike returns true for MIME types or extensions treated as UTF-8 text.
func isTextLike(mime, ext string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	// SVG is XML markup: a model can reason about the image from its source
	// even when no raster path is available (ADR-051 RD1 extension, Option B).
	if mime == "image/svg+xml" {
		return true
	}
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".tsv", ".json", ".yaml", ".yml",
		".xml", ".svg", ".html", ".htm", ".toml", ".ini", ".cfg", ".conf",
		".go", ".py", ".js", ".ts", ".tsx", ".jsx", ".rs", ".rb", ".java",
		".c", ".h", ".cpp", ".cc", ".cxx", ".cs", ".swift", ".kt",
		".sh", ".bash", ".zsh", ".fish", ".ps1",
		".sql", ".graphql", ".proto", ".tf", ".hcl",
		".r", ".m", ".lua", ".pl", ".php":
		return true
	}
	return false
}

// isOOXML returns true for Office Open XML formats.
func isOOXML(mime, ext string) bool {
	switch ext {
	case ".docx", ".pptx", ".xlsx":
		return true
	}
	switch mime {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
		"application/vnd.openxmlformats-officedocument.presentationml.presentation",
		"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return true
	}
	return false
}

// ooxmlExtensionForMIME maps a recognized OOXML MIME to its extractor suffix.
func ooxmlExtensionForMIME(mime, fallback string) string {
	switch mime {
	case "application/vnd.openxmlformats-officedocument.wordprocessingml.document":
		return ".docx"
	case "application/vnd.openxmlformats-officedocument.presentationml.presentation":
		return ".pptx"
	case "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet":
		return ".xlsx"
	default:
		return fallback
	}
}

func extractOOXMLBytes(data []byte, ext string) (string, bool, string) {
	count, _, _, err := zipDirectoryInfo(data)
	if err != nil {
		return "", false, fmt.Sprintf("zip open failed: %v", err)
	}
	if count > maxArchiveEntries {
		return "", false, fmt.Sprintf("OOXML archive has too many entries (limit %d)", maxArchiveEntries)
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", false, fmt.Sprintf("zip open failed: %v", err)
	}
	var decompressedTotal uint64
	for _, f := range zr.File {
		if f.UncompressedSize64 > maxDecompressedBytes-decompressedTotal {
			return "", false, fmt.Sprintf("OOXML archive exceeds %d byte decompression limit", maxDecompressedBytes)
		}
		decompressedTotal += f.UncompressedSize64
	}
	return extractOOXML(zr, ext)
}

// detectOOXML identifies an OOXML package by its ZIP signature and the document
// content type declared in [Content_Types].xml.
func detectOOXML(data []byte) (string, bool) {
	if len(data) < 4 || !bytes.Equal(data[:4], []byte("PK\x03\x04")) {
		return "", false
	}
	count, _, _, err := zipDirectoryInfo(data)
	if err != nil || count > maxArchiveEntries {
		return "", false
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", false
	}
	for _, f := range zr.File {
		if f.Name != "[Content_Types].xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", false
		}
		raw, readErr := io.ReadAll(io.LimitReader(rc, 1<<20))
		closeErr := rc.Close()
		if readErr != nil || closeErr != nil {
			return "", false
		}
		content := strings.ToLower(string(raw))
		switch {
		case strings.Contains(content, "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"):
			return ".docx", true
		case strings.Contains(content, "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml"):
			return ".pptx", true
		case strings.Contains(content, "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"):
			return ".xlsx", true
		default:
			return "", false
		}
	}
	return "", false
}

// isPDF returns true for PDF files.
func isPDF(mime, ext string) bool {
	return ext == ".pdf" || mime == "application/pdf"
}

type archiveManifestEntry struct {
	name      string
	size      uint64
	protected bool
}

func archiveKindForMIME(mime string) string {
	switch mime {
	case "application/zip", "application/x-zip-compressed":
		return "zip"
	case "application/x-tar":
		return "tar"
	case "application/gzip", "application/x-gzip":
		return "gzip"
	default:
		return ""
	}
}

func archiveKindForFilename(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".tar.gz"), strings.HasSuffix(lower, ".tgz"):
		return "targz"
	case strings.HasSuffix(lower, ".zip"):
		return "zip"
	case strings.HasSuffix(lower, ".tar"):
		return "tar"
	case strings.HasSuffix(lower, ".gz"):
		return "gzip"
	default:
		return ""
	}
}

// extractArchive dispatches by recognized MIME before consulting the filename.
func extractArchive(data []byte, mime, filename string) (string, bool, string) {
	kind := archiveKindForMIME(mime)
	if kind == "" {
		kind = archiveKindForFilename(filename)
	}

	switch kind {
	case "zip":
		return extractZIPManifest(data)
	case "tar":
		return extractTARManifest(data)
	case "targz":
		return extractGZIP(data, true)
	case "gzip":
		return extractGZIP(data, false)
	default:
		return "", false, "unsupported archive format"
	}
}

// zipDirectoryInfo reads the EOCD record without constructing zip.File values,
// allowing the entry cap to be checked before central-directory allocation.
func zipDirectoryInfo(data []byte) (count int, directoryOffset int, directorySize int, err error) {
	const (
		eocdSignature = 0x06054b50
		eocdSize      = 22
		maxComment    = 1<<16 - 1
	)
	if len(data) < eocdSize {
		return 0, 0, 0, fmt.Errorf("end-of-central-directory record not found")
	}
	start := len(data) - eocdSize
	minimum := max(0, len(data)-eocdSize-maxComment)
	for pos := start; pos >= minimum; pos-- {
		if binary.LittleEndian.Uint32(data[pos:pos+4]) != eocdSignature {
			continue
		}
		commentLen := int(binary.LittleEndian.Uint16(data[pos+20 : pos+22]))
		if pos+eocdSize+commentLen != len(data) {
			continue
		}
		if binary.LittleEndian.Uint16(data[pos+4:pos+6]) != 0 || binary.LittleEndian.Uint16(data[pos+6:pos+8]) != 0 {
			return 0, 0, 0, fmt.Errorf("multi-disk ZIP archives are unsupported")
		}
		diskCount := binary.LittleEndian.Uint16(data[pos+8 : pos+10])
		totalCount := binary.LittleEndian.Uint16(data[pos+10 : pos+12])
		if diskCount != totalCount {
			return 0, 0, 0, fmt.Errorf("inconsistent ZIP entry counts")
		}
		if totalCount == ^uint16(0) {
			return 0, 0, 0, fmt.Errorf("ZIP64 archives are unsupported")
		}
		directorySize = int(binary.LittleEndian.Uint32(data[pos+12 : pos+16]))
		directoryOffset = int(binary.LittleEndian.Uint32(data[pos+16 : pos+20]))
		if directoryOffset < 0 || directorySize < 0 || directoryOffset > pos || directorySize > pos-directoryOffset {
			return 0, 0, 0, fmt.Errorf("invalid central-directory bounds")
		}
		return int(totalCount), directoryOffset, directorySize, nil
	}
	return 0, 0, 0, fmt.Errorf("end-of-central-directory record not found")
}

func extractZIPManifest(data []byte) (string, bool, string) {
	count, offset, size, err := zipDirectoryInfo(data)
	if err != nil {
		return "", false, fmt.Sprintf("zip manifest failed: %v", err)
	}

	const centralHeaderSize = 46
	entries := make([]archiveManifestEntry, 0, min(count, maxArchiveEntries))
	pos := offset
	end := offset + size
	for i := 0; i < count; i++ {
		if pos > end-centralHeaderSize || binary.LittleEndian.Uint32(data[pos:pos+4]) != 0x02014b50 {
			return "", false, "zip manifest failed: corrupt central directory"
		}
		flags := binary.LittleEndian.Uint16(data[pos+8 : pos+10])
		uncompressed := binary.LittleEndian.Uint32(data[pos+24 : pos+28])
		nameLen := int(binary.LittleEndian.Uint16(data[pos+28 : pos+30]))
		extraLen := int(binary.LittleEndian.Uint16(data[pos+30 : pos+32]))
		commentLen := int(binary.LittleEndian.Uint16(data[pos+32 : pos+34]))
		recordLen := centralHeaderSize + nameLen + extraLen + commentLen
		if recordLen < centralHeaderSize || pos > end-recordLen {
			return "", false, "zip manifest failed: corrupt central-directory record"
		}
		if i < maxArchiveEntries {
			entries = append(entries, archiveManifestEntry{
				name:      truncateName(string(data[pos+centralHeaderSize : pos+centralHeaderSize+nameLen])),
				size:      uint64(uncompressed),
				protected: flags&1 != 0,
			})
		}
		pos += recordLen
	}
	if pos != end {
		return "", false, "zip manifest failed: central-directory size mismatch"
	}
	return renderArchiveManifest("zip", entries, count), true, ""
}

func extractTARManifest(data []byte) (string, bool, string) {
	tr := tar.NewReader(bytes.NewReader(data))
	entries := make([]archiveManifestEntry, 0, min(maxArchiveEntries, 32))
	total := 0
	var representedBytes uint64
	capped := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false, fmt.Sprintf("tar manifest failed: %v", err)
		}
		total++
		if len(entries) >= maxArchiveEntries || hdr.Size < 0 ||
			representedBytes+uint64(hdr.Size) > maxDecompressedBytes {
			capped = true
			continue
		}
		representedBytes += uint64(hdr.Size)
		entries = append(entries, archiveManifestEntry{name: truncateName(hdr.Name), size: uint64(hdr.Size)})
	}
	if total == 0 {
		return "", false, "tar manifest failed: archive contains no entries"
	}
	if capped && total == len(entries) {
		total++
	}
	return renderArchiveManifest("tar", entries, total), true, ""
}

func extractGZIP(data []byte, forceTar bool) (string, bool, string) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return "", false, fmt.Sprintf("gzip open failed: %v", err)
	}
	raw, readErr := io.ReadAll(io.LimitReader(gz, maxDecompressedBytes+1))
	closeErr := gz.Close()
	if readErr != nil {
		return "", false, fmt.Sprintf("gzip read failed: %v", readErr)
	}
	if closeErr != nil {
		return "", false, fmt.Sprintf("gzip close failed: %v", closeErr)
	}

	truncated := len(raw) > maxDecompressedBytes
	if truncated {
		raw = raw[:maxDecompressedBytes]
	}
	isTar := len(raw) >= 262 && string(raw[257:262]) == "ustar"
	if forceTar || isTar {
		text, ok, reason := extractTARManifest(raw)
		if truncated {
			if !ok {
				return "[truncated: gzip stream exceeds 25 MiB cap]", true, ""
			}
			text += "\n[truncated: gzip stream exceeds 25 MiB cap]"
		}
		return text, ok, reason
	}

	text, ok, reason := extractTextBytes(raw)
	if truncated {
		if !ok {
			return "[truncated: gzip stream exceeds 25 MiB cap]", true, ""
		}
		text += "\n[truncated: gzip stream exceeds 25 MiB cap]"
	}
	return text, ok, reason
}

func renderArchiveManifest(kind string, entries []archiveManifestEntry, total int) string {
	header := fmt.Sprintf("Archive manifest (%s):", kind)
	lines := make([]string, 0, len(entries))
	used := len(header)
	shown := 0
	var representedBytes uint64
	for _, entry := range entries {
		if representedBytes+entry.size > maxDecompressedBytes {
			break
		}
		line := fmt.Sprintf("\n- %s (%d bytes)", entry.name, entry.size)
		if entry.protected {
			line += " [protected]"
		}
		if used+len(line) > maxManifestBytes-128 {
			break
		}
		lines = append(lines, line)
		used += len(line)
		representedBytes += entry.size
		shown++
	}

	var sb strings.Builder
	sb.Grow(used + 96)
	sb.WriteString(header)
	for _, line := range lines {
		sb.WriteString(line)
	}
	if shown < total {
		fmt.Fprintf(&sb, "\n[truncated: showing first %d of %d entries]", shown, total)
	}
	return sb.String()
}

func filterControlCharacters(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf) {
			return -1
		}
		return r
	}, s)
}

func truncateName(name string) string {
	runes := []rune(filterControlCharacters(name))
	if len(runes) > maxArchiveNameLen {
		runes = runes[:maxArchiveNameLen]
	}
	return string(runes)
}

func extractTextBytes(raw []byte) (string, bool, string) {
	// Cap to the rune budget's worth of bytes (a rune is at most 4 bytes).
	if len(raw) > maxExtractRunes*4+1 {
		raw = raw[:maxExtractRunes*4+1]
	}

	if len(raw) == 0 {
		return "", false, "file is empty"
	}

	if !utf8.Valid(raw) {
		return "", false, "file is not valid UTF-8"
	}

	s := filterControlCharacters(string(raw))
	runes := []rune(s)
	truncated := false
	if len(runes) > maxExtractRunes {
		runes = runes[:maxExtractRunes]
		truncated = true
	}
	result := string(runes)
	if truncated {
		result += "\n…[truncated]"
	}
	return result, true, ""
}

// extractPDF reads text from a PDF using ledongthuc/pdf (pure Go, no CGo).
// It reads from r (size bytes) so the same core serves both the path-based
// Extract and the in-memory ExtractBytes without re-opening a file.
func extractPDF(r io.ReaderAt, size int64) (string, bool, string) {
	pr, err := pdf.NewReader(r, size)
	if err != nil {
		return "", false, fmt.Sprintf("pdf open failed: %v", err)
	}

	plainReader, err := pr.GetPlainText()
	if err != nil {
		return "", false, fmt.Sprintf("pdf text extraction failed: %v", err)
	}

	raw, err := io.ReadAll(io.LimitReader(plainReader, int64(maxExtractRunes*4+1)))
	if err != nil {
		return "", false, fmt.Sprintf("pdf read failed: %v", err)
	}

	if len(raw) == 0 {
		return "", false, "pdf contains no extractable text"
	}

	s := filterControlCharacters(string(raw))
	runes := []rune(s)
	truncated := false
	if len(runes) > maxExtractRunes {
		runes = runes[:maxExtractRunes]
		truncated = true
	}
	result := string(runes)
	if truncated {
		result += "\n…[truncated]"
	}
	return result, true, ""
}

var errArchiveDecompressionLimit = errors.New("archive decompression limit exceeded")

type archiveReadBudget struct {
	remaining int64
}

type archiveBudgetReader struct {
	r      io.Reader
	budget *archiveReadBudget
}

func (r *archiveBudgetReader) Read(p []byte) (int, error) {
	if r.budget.remaining <= 0 {
		var probe [1]byte
		n, err := r.r.Read(probe[:])
		if n > 0 {
			return 0, errArchiveDecompressionLimit
		}
		return 0, err
	}
	if int64(len(p)) > r.budget.remaining {
		p = p[:r.budget.remaining]
	}
	n, err := r.r.Read(p)
	r.budget.remaining -= int64(n)
	return n, err
}

func (b *archiveReadBudget) reader(r io.Reader) io.Reader {
	return &archiveBudgetReader{r: r, budget: b}
}

// extractOOXML dispatches to the format-specific OOXML extractor based on ext.
// zr is an already-open zip archive (from a file or an in-memory buffer).
func extractOOXML(zr *zip.Reader, ext string) (string, bool, string) {
	budget := &archiveReadBudget{remaining: maxDecompressedBytes}
	switch ext {
	case ".docx":
		return extractDocx(zr, budget)
	case ".pptx":
		return extractPptx(zr, budget)
	case ".xlsx":
		return extractXlsx(zr, budget)
	default:
		return "", false, fmt.Sprintf("unknown OOXML extension: %s", ext)
	}
}

// extractDocx extracts text from a .docx file by parsing word/document.xml.
// It concatenates <w:t> runs and inserts a newline between paragraphs (<w:p>).
func extractDocx(zr *zip.Reader, budget *archiveReadBudget) (string, bool, string) {
	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", false, fmt.Sprintf("docx: open word/document.xml: %v", err)
		}
		defer rc.Close()
		return parseDocxXML(budget.reader(rc))
	}
	return "", false, "docx: word/document.xml not found"
}

type docxState int

const (
	docxOther docxState = iota
	docxInParagraph
)

// parseDocxXML parses word/document.xml and extracts paragraph text.
func parseDocxXML(r io.Reader) (string, bool, string) {
	dec := xml.NewDecoder(r)
	var sb strings.Builder
	var state docxState
	runeCount := 0

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", false, fmt.Sprintf("docx: xml decode error: %v", err)
		}

		switch t := tok.(type) {
		case xml.StartElement:
			localName := t.Name.Local
			switch localName {
			case "p": // w:p — paragraph start
				state = docxInParagraph
			case "br", "cr": // w:br, w:cr — line break within paragraph
				if runeCount < maxExtractRunes {
					sb.WriteByte('\n')
					runeCount++
				}
			}
		case xml.EndElement:
			if t.Name.Local == "p" && state == docxInParagraph {
				if runeCount < maxExtractRunes {
					sb.WriteByte('\n')
					runeCount++
				}
				state = docxOther
			}
		case xml.CharData:
			// Collect non-whitespace character data and preserve Word paragraph and break boundaries.
			s := filterControlCharacters(string(t))
			if strings.TrimSpace(s) == "" {
				continue
			}
			runes := []rune(s)
			if runeCount+len(runes) > maxExtractRunes {
				runes = runes[:maxExtractRunes-runeCount]
				sb.WriteString(string(runes))
				sb.WriteString("\n…[truncated]")
				return sb.String(), true, ""
			}
			sb.WriteString(s)
			runeCount += len(runes)
		}
	}

	if sb.Len() == 0 {
		return "", false, "docx: no text content found"
	}
	return strings.TrimSpace(sb.String()), true, ""
}

// extractPptx extracts text from a .pptx file by reading all slide XML files.
func extractPptx(zr *zip.Reader, budget *archiveReadBudget) (string, bool, string) {
	var sb strings.Builder
	runeCount := 0
	slideCount := 0

	for _, f := range zr.File {
		// Slide files are at ppt/slides/slide<N>.xml
		if !strings.HasPrefix(f.Name, "ppt/slides/slide") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		slideCount++
		rc, err := f.Open()
		if err != nil {
			continue
		}
		text, err := parsePptxSlide(budget.reader(rc), maxExtractRunes-runeCount)
		rc.Close()
		if err != nil {
			continue
		}
		if text != "" {
			sb.WriteString(text)
			sb.WriteByte('\n')
			runeCount += len([]rune(text)) + 1
		}
		if runeCount >= maxExtractRunes {
			sb.WriteString("…[truncated]")
			return sb.String(), true, ""
		}
	}

	if slideCount == 0 {
		return "", false, "pptx: no slide files found"
	}
	if sb.Len() == 0 {
		return "", false, "pptx: no text content found"
	}
	return strings.TrimSpace(sb.String()), true, ""
}

// parsePptxSlide extracts <a:t> text runs from a single slide XML.
func parsePptxSlide(r io.Reader, runeLimit int) (string, error) {
	dec := xml.NewDecoder(r)
	var sb strings.Builder
	runeCount := 0

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return sb.String(), err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "p" { // a:p — paragraph boundary
				if runeCount > 0 {
					sb.WriteByte('\n')
					runeCount++
				}
			}
		case xml.CharData:
			s := filterControlCharacters(string(t))
			if strings.TrimSpace(s) == "" {
				continue
			}
			runes := []rune(s)
			if runeCount+len(runes) > runeLimit {
				runes = runes[:runeLimit-runeCount]
				sb.WriteString(string(runes))
				return sb.String(), nil
			}
			sb.WriteString(s)
			runeCount += len(runes)
		}
	}
	return sb.String(), nil
}

// extractXlsx extracts cell values from an .xlsx file.
// It reads xl/sharedStrings.xml (string pool) and xl/worksheets/sheet*.xml (cells).
func extractXlsx(zr *zip.Reader, budget *archiveReadBudget) (string, bool, string) {
	// Step 1: load shared strings pool.
	sharedStrings, err := loadXlsxSharedStrings(zr, budget)
	if err != nil {
		return "", false, fmt.Sprintf("xlsx: shared strings: %v", err)
	}

	// Step 2: collect all sheet files.
	var sb strings.Builder
	runeCount := 0
	sheetCount := 0

	for _, f := range zr.File {
		if !strings.HasPrefix(f.Name, "xl/worksheets/sheet") || !strings.HasSuffix(f.Name, ".xml") {
			continue
		}
		sheetCount++
		rc, err := f.Open()
		if err != nil {
			continue
		}
		text, err := parseXlsxSheet(budget.reader(rc), sharedStrings, maxExtractRunes-runeCount)
		rc.Close()
		if err != nil {
			continue
		}
		if text != "" {
			sb.WriteString(text)
			sb.WriteByte('\n')
			runeCount += len([]rune(text)) + 1
		}
		if runeCount >= maxExtractRunes {
			sb.WriteString("…[truncated]")
			return sb.String(), true, ""
		}
	}

	if sheetCount == 0 {
		return "", false, "xlsx: no sheet files found"
	}
	if sb.Len() == 0 {
		return "", false, "xlsx: no text content found"
	}
	return strings.TrimSpace(sb.String()), true, ""
}

// loadXlsxSharedStrings reads the xl/sharedStrings.xml string table from the zip.
// Returns a slice where index corresponds to the shared-string index.
func loadXlsxSharedStrings(zr *zip.Reader, budget *archiveReadBudget) ([]string, error) {
	for _, f := range zr.File {
		if f.Name != "xl/sharedStrings.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return parseSharedStrings(budget.reader(rc))
	}
	// No shared strings file is valid when all cells are inline values.
	return nil, nil
}

// parseSharedStrings parses xl/sharedStrings.xml and returns the string array.
func parseSharedStrings(r io.Reader) ([]string, error) {
	dec := xml.NewDecoder(r)
	var result []string
	var current strings.Builder
	inSI := false

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return result, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "si" {
				inSI = true
				current.Reset()
			}
		case xml.EndElement:
			if t.Name.Local == "si" && inSI {
				if len(result) >= maxArchiveEntries {
					return nil, fmt.Errorf("shared string count exceeds %d", maxArchiveEntries)
				}
				result = append(result, current.String())
				inSI = false
			}
		case xml.CharData:
			if inSI {
				current.WriteString(filterControlCharacters(string(t)))
				if current.Len() > maxExtractRunes*4 {
					return nil, fmt.Errorf("shared string exceeds %d byte limit", maxExtractRunes*4)
				}
			}
		}
	}
	return result, nil
}

// parseXlsxSheet reads one worksheet XML and returns its cell values as tab-separated rows.
func parseXlsxSheet(r io.Reader, sharedStrings []string, runeLimit int) (string, error) {
	dec := xml.NewDecoder(r)
	var sb strings.Builder
	runeCount := 0
	inRow := false
	inCell := false
	cellType := ""
	cellValue := ""
	firstCellInRow := true

	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return sb.String(), err
		}

		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "row":
				inRow = true
				firstCellInRow = true
			case "c":
				if inRow {
					inCell = true
					cellType = ""
					cellValue = ""
					for _, attr := range t.Attr {
						if attr.Name.Local == "t" {
							cellType = attr.Value
						}
					}
				}
			case "v", "t": // cell value or inline string text
			}
		case xml.EndElement:
			switch t.Name.Local {
			case "row":
				if inRow {
					sb.WriteByte('\n')
					runeCount++
					inRow = false
				}
			case "c":
				if inCell {
					// Resolve shared string if type is "s"
					displayVal := cellValue
					if cellType == "s" && sharedStrings != nil {
						var idx int
						for _, ch := range cellValue {
							if ch >= '0' && ch <= '9' {
								idx = idx*10 + int(ch-'0')
							}
						}
						if idx < len(sharedStrings) {
							displayVal = sharedStrings[idx]
						}
					}
					if displayVal != "" {
						if !firstCellInRow {
							sb.WriteByte('\t')
							runeCount++
						}
						runes := []rune(displayVal)
						if runeCount+len(runes) > runeLimit {
							runes = runes[:runeLimit-runeCount]
							sb.WriteString(string(runes))
							return sb.String(), nil
						}
						sb.WriteString(displayVal)
						runeCount += len(runes)
						firstCellInRow = false
					}
					inCell = false
				}
			case "v", "t":
			}
		case xml.CharData:
			if inCell {
				cellValue += filterControlCharacters(string(t))
			}
		}
	}
	return sb.String(), nil
}
