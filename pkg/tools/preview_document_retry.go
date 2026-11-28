// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import (
	"context"

	"github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/media"
	"github.com/elicify-ai/omnipus/pkg/preview"
)

// PreviewRetryGroup is the person-only thumbnail group of a preview Retry
// (office-preview spec §5.2, FQ2 r2 A): res's pages stored exactly as the
// tool stores them (session-scoped when ctx carries the transcript session
// id) and the preview record. Nothing of it reaches a model. It writes the
// run's preview_document.run audit record with auditPath as the source.
func PreviewRetryGroup(ctx context.Context, store media.MediaStore, auditLog *audit.Logger, name, auditPath string, res *preview.Result) ([]DocumentPreviewThumbnail, generated.MediaPreview) {
	t := &PreviewDocumentTool{mediaStore: store, auditLogger: auditLog}
	emitPreviewRunAudit(ctx, auditLog, t.Name(), auditPath, res)
	doc := t.buildResult(ctx, name, res, nil).DocumentPreview
	return doc.Thumbnails, doc.Record
}
