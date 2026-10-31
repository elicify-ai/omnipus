// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agent

import (
	"strings"

	"github.com/elicify-ai/omnipus/pkg/bus"
	"github.com/elicify-ai/omnipus/pkg/logger"
	"github.com/elicify-ai/omnipus/pkg/steer"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

const (
	previewPresentationPageThumbnails = "page_thumbnails"
	previewWebChannel                 = "webchat"
)

// deliverPreviewThumbnails publishes a preview_document call's thumbnail
// group to the web chat (office-preview spec §5.1/§5.2). The thumbnails are
// display-only: they never enter ToolResult.Media, so model-media
// admission never sees them. Other channels receive no thumbnails (§4.3
// "Non-web channel").
func (ex *agentLoopRunTurnToolsExecute) deliverPreviewThumbnails() {
	doc := ex.toolResult.DocumentPreview
	rt := ex.rx.rr.rq.ri.rf.rt
	if doc == nil || ex.toolResult.IsError || rt.ts.channel != previewWebChannel {
		return
	}
	parts := make([]bus.MediaPart, 0, len(doc.Thumbnails))
	for _, th := range doc.Thumbnails {
		parts = append(parts, bus.MediaPart{
			Type: "image", Ref: th.Ref, Caption: th.Caption, Filename: th.Filename,
			ContentType: th.ContentType, PageNumber: th.PageNumber,
		})
	}
	record := doc.Record
	msg := bus.OutboundMediaMessage{
		AgentID:      rt.ts.agent.ID,
		Channel:      rt.ts.channel,
		ChatID:       rt.ts.chatID,
		WorkspaceID:  rt.ts.opts.WorkspaceID,
		SessionID:    rt.ts.transcriptSessionID,
		Parts:        parts,
		ToolCallID:   ex.toolCallID,
		Presentation: previewPresentationPageThumbnails,
		Preview:      &record,
	}
	if rt.al.audienceFor(ex.rx.ctx, steer.BoundaryMedia, rt.ts.transcriptSessionID) != steer.AudienceUser {
		return
	}
	if ex.rx.turnChannelManager != nil {
		if err := ex.rx.turnChannelManager.SendMedia(ex.rx.ctx, msg); err != nil {
			// The model's result already states the stored outcome; a
			// display delivery failure is logged, the group replays from
			// the transcript.
			logger.WarnCF("agent", "Failed to deliver preview thumbnails",
				map[string]any{"tool_call_id": ex.toolCallID, "error": err.Error()})
		}
		return
	}
	if rt.al.bus != nil {
		rt.al.bus.PublishOutboundMedia(ex.rx.ctx, msg)
	}
}

// previewToolRecord is the persisted tool-call result of a preview_document
// call: the thumbnail descriptors, presentation and preview record replay
// reads back (pkg/gateway/replay.go::buildMediaFrame), plus the result text.
func previewToolRecord(doc *tools.DocumentPreview, resultText string) map[string]any {
	descs := make([]map[string]any, 0, len(doc.Thumbnails))
	for _, th := range doc.Thumbnails {
		descs = append(descs, map[string]any{
			"ref": th.Ref, "filename": th.Filename, "content_type": th.ContentType,
			"type": "image", "caption": th.Caption, "page_number": th.PageNumber,
		})
	}
	preview := map[string]any{
		"source_name":    doc.Record.SourceName,
		"source_version": doc.Record.SourceVersion,
		"page_count":     doc.Record.PageCount,
		"first_page":     doc.Record.FirstPage,
		"last_page":      doc.Record.LastPage,
		"outcome":        doc.Record.Outcome,
	}
	if doc.Record.UnavailableReason != nil {
		preview["unavailable_reason"] = *doc.Record.UnavailableReason
	}
	result := map[string]any{
		"media":        descs,
		"presentation": previewPresentationPageThumbnails,
		"preview":      preview,
	}
	if text := strings.TrimSpace(resultText); text != "" {
		result["text"] = text
	}
	return result
}
