package tools

// read_pdf.go — read_file / library_read's PDF path (issue #1968, spec
// FR-5..FR-8). A `.pdf` name or a `%PDF-` magic routes here before the
// binary (NUL) check. The persisted result is always text (or a notice):
// the PDF bytes themselves ride only as a turn-private InspectionPDFs item
// that the agent presents natively, per provider attempt, to models that
// support PDFs (FR-10..FR-14).

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/docextract"
)

// PDFNativeNotice is notice R1 (spec §5.6): the persisted result says the
// PDF itself goes to PDF-capable models this turn only.
const PDFNativeNotice = "[PDF: sent natively to models that support PDFs, within their limits, this turn only; later turns see this text]"

// PDFNoticeScanned is notice N1 (spec §5.6): the PDF parses but has no
// extractable text. The one source of N1 for the persisted read result and
// the agent's chat-attachment presentation.
func PDFNoticeScanned(name string) string {
	return fmt.Sprintf("[document: %s | no extractable text (likely scanned) | readable natively by PDF-capable models; otherwise convert pages to PNG/JPEG]", name)
}

// PDFNoticeMediaSize is notice L1 (spec §5.6): a PDF over the operator's
// max_media_size setting is shown as text and never sent natively. The one
// source of L1 for read_file / library_read and chat attachments.
func PDFNoticeMediaSize(maxBytes int64) string {
	return fmt.Sprintf("[PDF over the %d-byte media size setting; not sent natively]", maxBytes)
}

// PDFNoticeLocalFailure is notice N3 (spec §5.6): the local parser could
// not read the PDF, with InspectPDF's typed reason; the bytes still go to
// PDF-capable models. The one source of N3 for the persisted read result
// and the agent's chat-attachment presentation.
func PDFNoticeLocalFailure(name string, inspectErr error) string {
	return fmt.Sprintf("[document: %s | could not be read locally (%s) | sent as is to PDF-capable models]",
		name, pdfLocalFailureReason(inspectErr))
}

// isPDFRead reports whether a read must take the PDF path (FR-5): the name
// ends in .pdf (any case) or the content starts with the %PDF- magic.
func isPDFRead(path string, sniff []byte) bool {
	return strings.EqualFold(filepath.Ext(path), ".pdf") || docextract.IsPDFMagic(sniff)
}

// pdfLocalFailureReason maps InspectPDF's typed error to N3's reason text.
// A refusal or timeout of the shared PDF parse boundary is this process's
// load, not a fault of the file, so it gets its own reason instead of
// "malformed".
func pdfLocalFailureReason(err error) string {
	switch {
	case errors.Is(err, docextract.ErrPDFEncrypted):
		return "encrypted"
	case errors.Is(err, docextract.ErrPDFUnsupportedVersion):
		return "unsupported version"
	case errors.Is(err, docextract.ErrPDFBusy):
		return "parser busy, try again later"
	case errors.Is(err, docextract.ErrPDFTimedOut):
		return "timed out"
	default:
		return "malformed"
	}
}

// PDFTemporaryFailureReason returns the N3 reason text when inspectErr is a
// refusal or timeout of the shared PDF parse boundary (the process's load,
// not a property of the file), and "" for every other outcome, including
// success and a malformed, encrypted or unsupported file. Callers use it to
// keep "not inspected right now" apart from "inspected, no text".
func PDFTemporaryFailureReason(inspectErr error) string {
	if errors.Is(inspectErr, docextract.ErrPDFBusy) || errors.Is(inspectErr, docextract.ErrPDFTimedOut) {
		return pdfLocalFailureReason(inspectErr)
	}
	return ""
}

// readPDF implements FR-6..FR-8 for one PDF the caller already resolved,
// authorized and opened. It reads the caller's already-open, kind-checked
// descriptor (file, whose first bytes the caller consumed into sniff) and
// never reopens the path: a reopen could reach a different file, or block
// on a FIFO swapped in after the check (code-review finding I4).
func (t *ReadFileTool) readPDF(
	file io.Reader,
	sniff []byte,
	path string,
	offset, length int64,
	paginationSupplied bool,
	reauthorize func(context.Context) error,
) *ToolResult {
	raw, err := io.ReadAll(io.LimitReader(io.MultiReader(bytes.NewReader(sniff), file), docextract.MaxDocBytes+1))
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to read document: %v", err))
	}
	if int64(len(raw)) > docextract.MaxDocBytes {
		return ErrorResult(fmt.Sprintf(
			"document too large to extract (limit %d bytes); use a dedicated tool",
			docextract.MaxDocBytes,
		))
	}

	name := filepath.Base(path)
	if !docextract.IsPDFMagic(raw) {
		return ErrorResult(fmt.Sprintf(
			"%s is named like a PDF but does not start with the %%PDF- header, so it could not be read",
			name))
	}
	info, inspectErr := docextract.InspectPDF(raw)
	hasText := inspectErr == nil && info.Text != ""

	if paginationSupplied {
		switch {
		case inspectErr != nil: // E2
			return ErrorResult(fmt.Sprintf(
				"%s could not be read locally (%s), so offset/length have no text to page; "+
					"read it without offset/length to send it to models that support PDFs",
				name, pdfLocalFailureReason(inspectErr)))
		case !hasText: // E3
			return ErrorResult(fmt.Sprintf(
				"%s has no extractable text (likely scanned), so offset/length have no text to page; "+
					"read it without offset/length to send it to models that support PDFs, or convert its pages to PNG/JPEG",
				name))
		}
		return NewToolResult(pdfTextView(name, info, offset, length, ""))
	}

	if int64(len(raw)) > t.maxInspectionImageBytes {
		// Over the media size setting nothing is sent natively. When the
		// parser was busy or timed out, the file was not inspected: say so,
		// with the retry advice, instead of claiming it has no text.
		if reason := PDFTemporaryFailureReason(inspectErr); reason != "" { // E1, not inspected
			return ErrorResult(fmt.Sprintf(
				"%s is over the %d-byte media size setting, so it is not sent natively, and it could not be read locally right now (%s); "+
					"read it again later",
				name, t.maxInspectionImageBytes, reason))
		}
		if !hasText { // E1
			return ErrorResult(fmt.Sprintf(
				"%s is over the %d-byte media size setting and has no extractable text, so its content is unavailable; "+
					"raise the media size setting or convert its pages to PNG/JPEG",
				name, t.maxInspectionImageBytes))
		}
		return NewToolResult(pdfTextView(name, info, 0, t.maxSize, PDFNoticeMediaSize(t.maxInspectionImageBytes)))
	}

	sum := sha256.Sum256(raw)
	item := InspectionPDF{
		Bytes:        raw,
		Name:         name,
		Pages:        info.Pages,
		SHA256:       hex.EncodeToString(sum[:]),
		HasText:      hasText,
		LocalFailure: PDFTemporaryFailureReason(inspectErr),
		Reauthorize:  reauthorize,
	}
	var content string
	switch {
	case inspectErr != nil: // N3
		item.Pages = 0
		content = PDFNoticeLocalFailure(name, inspectErr) + "\n" + PDFNativeNotice
	case !hasText: // N1
		content = PDFNoticeScanned(name) + "\n" + PDFNativeNotice
	default:
		content = pdfTextView(name, info, 0, t.maxSize, PDFNativeNotice)
	}
	result := NewToolResult(content)
	result.InspectionPDFs = []InspectionPDF{item}
	return result
}

// pdfTextView renders the persisted text view: the document header, an
// optional notice line, then the extracted characters [offset,
// offset+length). Every framing line starts with "[document:" or "[PDF"
// so readers can tell notices from text.
func pdfTextView(name string, info docextract.PDFInfo, offset, length int64, notice string) string {
	runes := []rune(info.Text)
	total := int64(len(runes))
	start := min(max(offset, 0), total)
	end := min(start+length, total)
	pages := "unknown"
	if info.Pages > 0 {
		pages = fmt.Sprintf("%d", info.Pages)
	}
	var b strings.Builder
	if start >= total {
		fmt.Fprintf(&b, "[document: %s | extracted text | no characters at offset %d of %d | %s pages]", name, offset, total, pages)
		return b.String()
	}
	fmt.Fprintf(&b, "[document: %s | extracted text | chars %d-%d of %d | %s pages]", name, start, end-1, total, pages)
	if end < total {
		fmt.Fprintf(&b, "\n[document: %s | more text follows; re-read with offset=%d to continue]", name, end)
	}
	if notice != "" {
		b.WriteString("\n" + notice)
	}
	b.WriteString("\n" + string(runes[start:end]))
	return b.String()
}
