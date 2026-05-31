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
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// maxExtractRunes is the maximum number of Unicode code points returned.
// Extraction is stopped after this limit and a truncation notice is appended.
const maxExtractRunes = 100_000

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
	ext := strings.ToLower(filepath.Ext(filename))
	lowerMIME := strings.ToLower(mime)

	switch {
	case isTextLike(lowerMIME, ext):
		return extractText(path)
	case isOOXML(lowerMIME, ext):
		return extractOOXML(path, ext)
	case isPDF(lowerMIME, ext):
		return extractPDF(path)
	default:
		return "", false, fmt.Sprintf("unsupported format (mime=%s, ext=%s)", mime, ext)
	}
}

// isTextLike returns true for MIME types or extensions treated as UTF-8 text.
func isTextLike(mime, ext string) bool {
	if strings.HasPrefix(mime, "text/") {
		return true
	}
	switch ext {
	case ".txt", ".md", ".markdown", ".csv", ".tsv", ".json", ".yaml", ".yml",
		".xml", ".html", ".htm", ".toml", ".ini", ".cfg", ".conf",
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

// isPDF returns true for PDF files.
func isPDF(mime, ext string) bool {
	return ext == ".pdf" || mime == "application/pdf"
}

// extractText reads the file, validates it as UTF-8, and returns the content.
func extractText(path string) (string, bool, string) {
	f, err := os.Open(path)
	if err != nil {
		return "", false, fmt.Sprintf("open failed: %v", err)
	}
	defer f.Close()

	raw, err := io.ReadAll(io.LimitReader(f, int64(maxExtractRunes*4+1)))
	if err != nil {
		return "", false, fmt.Sprintf("read failed: %v", err)
	}

	if len(raw) == 0 {
		return "", false, "file is empty"
	}

	if !utf8.Valid(raw) {
		return "", false, "file is not valid UTF-8"
	}

	s := string(raw)
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
func extractPDF(path string) (string, bool, string) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", false, fmt.Sprintf("pdf open failed: %v", err)
	}
	defer f.Close()

	plainReader, err := r.GetPlainText()
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

	s := string(raw)
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

// extractOOXML dispatches to the format-specific OOXML extractor based on ext.
func extractOOXML(path, ext string) (string, bool, string) {
	switch ext {
	case ".docx":
		return extractDocx(path)
	case ".pptx":
		return extractPptx(path)
	case ".xlsx":
		return extractXlsx(path)
	default:
		return "", false, fmt.Sprintf("unknown OOXML extension: %s", ext)
	}
}

// extractDocx extracts text from a .docx file by parsing word/document.xml.
// It concatenates <w:t> runs and inserts a newline between paragraphs (<w:p>).
func extractDocx(path string) (string, bool, string) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", false, fmt.Sprintf("docx: zip open failed: %v", err)
	}
	defer zr.Close()

	for _, f := range zr.File {
		if f.Name != "word/document.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return "", false, fmt.Sprintf("docx: open word/document.xml: %v", err)
		}
		defer rc.Close()
		return parseDocxXML(rc)
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
			// Only collect text under <w:t> elements. Because xml.CharData
			// arrives between tags, we rely on the parent tracking done by
			// the decoder's implicit depth. Since we only read w:t runs here,
			// we track depth differently: parent tracking via the decoder itself.
			// The simpler approach: collect all CharData — field-level noise
			// (run properties, style names) is mostly empty strings and the
			// actual text content dominates. We skip whitespace-only tokens to
			// avoid noise from indented XML.
			s := string(t)
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
func extractPptx(path string) (string, bool, string) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", false, fmt.Sprintf("pptx: zip open failed: %v", err)
	}
	defer zr.Close()

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
		text, err := parsePptxSlide(rc, maxExtractRunes-runeCount)
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
			s := string(t)
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
func extractXlsx(path string) (string, bool, string) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", false, fmt.Sprintf("xlsx: zip open failed: %v", err)
	}
	defer zr.Close()

	// Step 1: load shared strings pool.
	sharedStrings, err := loadXlsxSharedStrings(zr)
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
		text, err := parseXlsxSheet(rc, sharedStrings, maxExtractRunes-runeCount)
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
func loadXlsxSharedStrings(zr *zip.ReadCloser) ([]string, error) {
	for _, f := range zr.File {
		if f.Name != "xl/sharedStrings.xml" {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, err
		}
		defer rc.Close()
		return parseSharedStrings(rc)
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
				result = append(result, current.String())
				inSI = false
			}
		case xml.CharData:
			if inSI {
				current.WriteString(string(t))
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
				cellValue += string(t)
			}
		}
	}
	return sb.String(), nil
}
