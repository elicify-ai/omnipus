// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/docextract"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/preview"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// isPDFSource reports a PDF by its signature (a ".pdf" name alone keeps
// today's path, so a mislabelled text file still reads as text).
func isPDFSource(head []byte) bool {
	return bytes.HasPrefix(head, []byte("%PDF-"))
}

// pdfDocumentResult is read_file on a PDF without offset/length (office
// preview spec §4.1, FR-039): the retained result is today's extracted text;
// the live model form is chosen per active model when the request is built
// (pkg/agent attachDocumentPreview): the whole PDF for a model that reads PDF
// natively, pages 1–10 as images for a model that only sees images, the text
// otherwise. A PDF that cannot be read as a whole again falls back to the
// text alone, with a log line. nil means "not a readable PDF": the caller
// keeps today's path for the file (raw text, or the binary-file refusal).
func (t *ReadFileTool) pdfDocumentResult(ctx context.Context, policy fspolicy.FSPolicy, handle *PathHandle, path, sourceIdentity string, length int64, reauthorize func(context.Context) error) *ToolResult {
	text := t.extractDocument(ctx, handle, path, 0, length)
	if text.IsError {
		return nil
	}
	raw, err := readWholePDF(handle)
	if err != nil {
		logger.WarnCF("tool", "read_file: PDF kept as text only (the native form could not be read)",
			map[string]any{"path": path, "error": err.Error()})
		return text
	}
	name := filepath.Base(path)
	text.DocumentPreview = &DocumentPreview{
		PDF:           raw,
		Reauthorize:   reauthorize,
		Record:        generated.MediaPreview{SourceName: name},
		ModelOnly:     true,
		NativeContent: fmt.Sprintf("[document: %s | PDF]", name),
		RenderPages:   t.pdfPageRenderer(policy, name, sourceIdentity, raw),
	}
	return text
}

func readWholePDF(handle *PathHandle) ([]byte, error) {
	file, err := handle.Open()
	if err != nil {
		return nil, err
	}
	defer file.Close()
	raw, err := io.ReadAll(io.LimitReader(file, docextract.MaxDocBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(raw)) > docextract.MaxDocBytes {
		return nil, fmt.Errorf("larger than %d bytes", docextract.MaxDocBytes)
	}
	if !bytes.HasPrefix(raw, []byte("%PDF-")) {
		return nil, fmt.Errorf("not a PDF")
	}
	return raw, nil
}

// pdfPageRenderer renders pages 1–10 through the shared preview service, under
// the calling turn's policy, with the same admission and disclosures as
// preview_document.
func (t *ReadFileTool) pdfPageRenderer(policy fspolicy.FSPolicy, name, source string, data []byte) func(context.Context) ([]DocumentPreviewPage, string, string) {
	return func(ctx context.Context) ([]DocumentPreviewPage, string, string) {
		svc := preview.Shared()
		if svc == nil {
			return nil, "document preview is not running", ""
		}
		callCtx, cancel := context.WithTimeout(ctx, preview.CallTimeout)
		defer cancel()
		adm, err := svc.Admit(callCtx, false)
		if err != nil {
			return nil, err.Error(), ""
		}
		defer adm.Release()
		readModel, err := sandbox.KernelPolicyForTurn(policy)
		if err != nil {
			return nil, "the sandbox policy for this turn could not be built: " + err.Error(), ""
		}
		res, err := svc.Preview(callCtx, adm, preview.Request{
			Name: name, Ext: ".pdf", Data: data,
			First: 1, Last: preview.DefaultPages, Default: true,
			ReadModel: readModel, CallerWriteRoots: preview.TurnWriteRoots(policy),
		})
		if err != nil {
			return nil, err.Error(), ""
		}
		emitPreviewRunAudit(ctx, t.auditLogger, t.Name(), source, res)
		pages := make([]DocumentPreviewPage, 0, len(res.Pages))
		for _, p := range res.Pages {
			pages = append(pages, DocumentPreviewPage{Number: p.Number, PNG: p.PNG})
		}
		var note strings.Builder
		if res.Last < res.PageCount {
			fmt.Fprintf(&note, "; call preview_document with pages \"%d-%d\"", res.Last+1, min(res.Last+preview.DefaultPages, res.PageCount))
		}
		if res.Unconfined != "" {
			note.WriteString("; converted without sandbox: " + res.Unconfined)
		}
		if res.MemoryUnenforced != "" {
			note.WriteString("; memory limit not enforced: " + res.MemoryUnenforced)
		}
		if len(pages) == 0 {
			unavailable := res.RenderDetail
			if unavailable == "" {
				unavailable = "the pages were not rendered"
			}
			return nil, unavailable, note.String()
		}
		return pages, "", note.String()
	}
}
