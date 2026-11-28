// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
	"github.com/elicify-ai/omnipus/pkg/preview"
)

// pdfPageRenderer renders pages 1–10 of a PDF read_file returned as an
// InspectionPDF (#1968 merge: one PDF path) through the shared preview
// service, with the calling turn's write roots as the writer exclusion
// (AD-4) and the same admission and disclosures as preview_document.
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
		res, err := svc.Preview(callCtx, adm, preview.Request{
			Name: name, Ext: ".pdf", Data: data,
			First: 1, Last: preview.DefaultPages, Default: true,
			CallerWriteRoots: preview.TurnWriteRoots(policy),
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
