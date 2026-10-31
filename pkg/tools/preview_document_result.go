// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/preview"
)

// DocumentPreview is one preview_document call's output beyond its text.
//
// The model forms (PDF, Pages, Text) are live-turn only: pkg/agent picks
// one per active model when it assembles a provider request (§4.1) and
// re-runs Reauthorize before each send; the transcript keeps only the
// result text. Thumbnails and Record form the display-only group for the
// person (§5.1, §5.2).
type DocumentPreview struct {
	// PDF is the whole PDF (the source, or the converted PDF).
	PDF []byte
	// Pages are the rendered page images (PNG), in page order.
	Pages []DocumentPreviewPage
	// Text is the extracted text form of the batch, with its notes.
	Text string
	// ImagesUnavailable is the "page images unavailable: <reason>" reason,
	// empty when pages were rendered.
	ImagesUnavailable string
	// Reauthorize re-checks the caller's read access to the source.
	Reauthorize func(context.Context) error

	// Thumbnails are the stored display images, in page order.
	Thumbnails []DocumentPreviewThumbnail
	// Record is the preview record of the thumbnail group.
	Record generated.MediaPreview
}

// DocumentPreviewPage is one rendered page.
type DocumentPreviewPage struct {
	Number int
	PNG    []byte
}

// DocumentPreviewThumbnail is one stored thumbnail.
type DocumentPreviewThumbnail struct {
	Ref         string
	Filename    string
	ContentType string
	Caption     string
	PageNumber  int
}

// LiveBytes is the size of the live model forms (AD-6 live-turn budget).
func (d *DocumentPreview) LiveBytes() int {
	if d == nil {
		return 0
	}
	n := len(d.PDF) + len(d.Text)
	for _, p := range d.Pages {
		n += len(p.PNG)
	}
	return n
}

// FreeLiveForms drops the live model forms (budget eviction); the display
// group is untouched.
func (d *DocumentPreview) FreeLiveForms() {
	if d == nil {
		return
	}
	d.PDF, d.Pages, d.Text = nil, nil, ""
}

// Preview outcome values (MediaFrame.preview.outcome).
const (
	PreviewOutcomeComplete    = "complete"
	PreviewOutcomePartial     = "partial"
	PreviewOutcomeUnavailable = "unavailable"
	previewReasonStorage      = "storage_failed"
	// DocumentPreviewNotRetained is the transcript marker for the live forms.
	DocumentPreviewNotRetained = "[document content is sent to the model for this turn only; not retained; call preview_document again]"
)

func signatureMatches(ext string, head []byte) bool {
	if ext == ".pdf" {
		return bytes.HasPrefix(head, []byte("%PDF-"))
	}
	// OOXML is a ZIP package; an encrypted OOXML document is a CFB file
	// (checked further after reading).
	return bytes.HasPrefix(head, []byte("PK\x03\x04")) ||
		bytes.HasPrefix(head, []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1})
}

type officeCheck int

const (
	officeOK officeCheck = iota
	officeMismatch
	officeEncrypted
)

// officeMainTypes are the main-part content types of the supported OOXML
// documents; macro-enabled and template variants carry other types.
var officeMainTypes = map[string]string{
	".docx": "application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml",
	".xlsx": "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml",
	".pptx": "application/vnd.openxmlformats-officedocument.presentationml.presentation.main+xml",
}

// officeContentCheck verifies an OOXML package declares the extension's
// main document type, and recognizes password-protected documents.
func officeContentCheck(ext string, data []byte) officeCheck {
	if bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0}) {
		if bytes.Contains(data, utf16le("EncryptedPackage")) {
			return officeEncrypted
		}
		return officeMismatch
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return officeMismatch
	}
	for _, f := range zr.File {
		if f.Name != "[Content_Types].xml" {
			continue
		}
		if f.UncompressedSize64 > 4<<20 {
			return officeMismatch
		}
		rc, err := f.Open()
		if err != nil {
			return officeMismatch
		}
		types, err := io.ReadAll(io.LimitReader(rc, 4<<20))
		_ = rc.Close()
		if err != nil {
			return officeMismatch
		}
		if bytes.Contains(types, []byte(`ContentType="`+officeMainTypes[ext]+`"`)) {
			return officeOK
		}
		return officeMismatch
	}
	return officeMismatch
}

func utf16le(s string) []byte {
	out := make([]byte, 0, 2*len(s))
	for _, r := range s {
		out = append(out, byte(r), 0)
	}
	return out
}

// buildResult turns a preview into the tool result: the text the model and
// transcript keep, the live forms and the stored thumbnail group.
func (t *PreviewDocumentTool) buildResult(ctx context.Context, name string, res *preview.Result, reauthorize func(context.Context) error) *ToolResult {
	doc := &DocumentPreview{
		PDF:         res.PDF,
		Reauthorize: reauthorize,
		Record: generated.MediaPreview{
			SourceName:    name,
			SourceVersion: res.SourceVersion,
			PageCount:     res.PageCount,
			FirstPage:     res.First,
			LastPage:      res.Last,
		},
	}
	for _, p := range res.Pages {
		doc.Pages = append(doc.Pages, DocumentPreviewPage{Number: p.Number, PNG: p.PNG})
	}
	doc.Text = previewTextForm(res)
	if len(res.Pages) == 0 {
		doc.ImagesUnavailable = res.RenderDetail
		if doc.ImagesUnavailable == "" {
			doc.ImagesUnavailable = "the pages were not rendered"
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s (version %s)\n", name, res.SourceVersion)
	fmt.Fprintf(&b, "pages %d–%d of %d", res.First, res.Last, res.PageCount)
	if res.Clamped {
		fmt.Fprintf(&b, " (requested %d–%d)", res.RequestedFirst, res.RequestedLast)
	}
	b.WriteString("\n")
	if res.Last < res.PageCount {
		next := min(res.Last+preview.DefaultPages, res.PageCount)
		fmt.Fprintf(&b, "next batch: call preview_document with pages \"%d-%d\"\n", res.Last+1, next)
	}
	if res.Unconfined != "" {
		fmt.Fprintf(&b, "converted without sandbox: %s\n", res.Unconfined)
	}
	if doc.ImagesUnavailable != "" {
		fmt.Fprintf(&b, "page images unavailable: %s\n", doc.ImagesUnavailable)
		doc.Record.Outcome = PreviewOutcomeUnavailable
		reason := res.RenderReason
		if reason == "" {
			reason = preview.ReasonRendererFailed
		}
		doc.Record.UnavailableReason = &reason
	} else {
		b.WriteString(t.storeThumbnails(ctx, name, res, doc))
	}
	b.WriteString(DocumentPreviewNotRetained)
	return &ToolResult{ForLLM: b.String(), DocumentPreview: doc}
}

// previewTextForm is the extracted-text model form (≤ 4 MiB, AD-15).
func previewTextForm(res *preview.Result) string {
	var b strings.Builder
	empty := true
	for _, p := range res.Text {
		if strings.TrimSpace(p.Text) != "" {
			empty = false
		}
		fmt.Fprintf(&b, "--- page %d ---\n%s\n", p.Number, p.Text)
	}
	switch {
	case res.Text == nil:
		return "[text extraction unavailable for this document]"
	case empty:
		return "[no extractable text — the PDF may be scanned]"
	}
	if res.TextTruncated {
		b.WriteString("[text truncated at the 4 MiB limit]\n")
	}
	return b.String()
}

// storeThumbnails stores the rendered pages like browser screenshots
// (§5.1) and returns the result-text line for the outcome.
func (t *PreviewDocumentTool) storeThumbnails(ctx context.Context, name string, res *preview.Result, doc *DocumentPreview) string {
	total := len(res.Pages)
	for _, p := range res.Pages {
		th, err := t.storeThumbnail(ctx, name, p, res.PageCount)
		if err != nil {
			logger.WarnCF("preview_document", "storing a page thumbnail failed",
				map[string]any{"page": p.Number, "error": err.Error()})
			continue
		}
		doc.Thumbnails = append(doc.Thumbnails, th)
	}
	stored := len(doc.Thumbnails)
	switch stored {
	case total:
		doc.Record.Outcome = PreviewOutcomeComplete
		return fmt.Sprintf("thumbnails stored: %d of %d\n", stored, total)
	case 0:
		doc.Record.Outcome = PreviewOutcomeUnavailable
		reason := previewReasonStorage
		doc.Record.UnavailableReason = &reason
		return "thumbnails unavailable: storage failed\n"
	default:
		doc.Record.Outcome = PreviewOutcomePartial
		return fmt.Sprintf("thumbnails stored: %d of %d\n", stored, total)
	}
}

func (t *PreviewDocumentTool) storeThumbnail(ctx context.Context, name string, p preview.Page, pageCount int) (DocumentPreviewThumbnail, error) {
	if t.mediaStore == nil {
		return DocumentPreviewThumbnail{}, fmt.Errorf("no media store")
	}
	sessionID := ToolTranscriptSessionID(ctx)
	dir, hasSession := media.SessionUploadsDir(sessionID)
	policy := media.CleanupPolicyForgetOnly
	scope := media.SessionInlineScopePrefix + sessionID
	if !hasSession {
		dir = media.TempDir()
		policy = media.CleanupPolicyDeleteOnCleanup
		scope = fmt.Sprintf("tool:inline:preview_document:%s:%s", ToolChannel(ctx), ToolChatID(ctx))
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return DocumentPreviewThumbnail{}, err
	}
	f, err := os.CreateTemp(dir, "tool-preview_document-*.png")
	if err != nil {
		return DocumentPreviewThumbnail{}, err
	}
	tmp := f.Name()
	if _, err = f.Write(p.PNG); err == nil {
		err = f.Close()
	} else {
		_ = f.Close()
	}
	if err != nil {
		_ = os.Remove(tmp)
		return DocumentPreviewThumbnail{}, err
	}
	filename := fmt.Sprintf("page-%d.png", p.Number)
	ref, err := t.mediaStore.Store(tmp, media.MediaMeta{
		Filename:      filename,
		ContentType:   "image/png",
		Source:        "tool:inline:preview_document",
		CleanupPolicy: policy,
	}, scope)
	if err != nil {
		_ = os.Remove(tmp)
		return DocumentPreviewThumbnail{}, err
	}
	return DocumentPreviewThumbnail{
		Ref:         ref,
		Filename:    filename,
		ContentType: "image/png",
		Caption:     fmt.Sprintf("%s · page %d of %d", name, p.Number, pageCount),
		PageNumber:  p.Number,
	}, nil
}
