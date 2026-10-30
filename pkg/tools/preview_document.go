// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package tools

import "context"

// PreviewDocumentTool is preview_document (office-preview spec AD-1, AD-13):
// it shows a .docx, .xlsx, .pptx or .pdf to the model in the best form the
// active model reads, and page thumbnails to the person.
//
// Wave 1 of the implementation lands only its identity — name, parameters,
// catalog entry and policy seeds — so the tool is governed and visible to the
// policy surface from the start. Execute refuses with a clear "not available
// yet" error until the renderer, converter and model-delivery paths land; it
// never pretends to have produced a preview.
type PreviewDocumentTool struct {
	BaseTool
}

// NewPreviewDocumentTool builds the tool with the read_file constructor
// convention (workspace root, restrict-to-workspace flag). Both arguments are
// unused until the read path lands with the preview pipeline.
func NewPreviewDocumentTool(_ string, _ bool) *PreviewDocumentTool {
	return &PreviewDocumentTool{}
}

func (t *PreviewDocumentTool) Name() string { return "preview_document" }

// Description is placeholder text owned by prometheus-prompt-engineer, who
// writes the final tool description (spec §18 W4).
func (t *PreviewDocumentTool) Description() string {
	return "Show a .docx, .xlsx, .pptx or .pdf file from the workspace so you can check how it looks. " +
		"Pages come in batches: pass pages as \"N\" or \"N-M\" (at most 20 pages per call); " +
		"without pages you get the first 10."
}

func (t *PreviewDocumentTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file": map[string]any{
				"type":        "string",
				"description": "Workspace path of the .docx, .xlsx, .pptx or .pdf file.",
			},
			"pages": map[string]any{
				"type":        "string",
				"description": "Optional page range: \"N\" or \"N-M\", at most 20 pages. Default: the first 10 pages.",
			},
		},
		"required": []string{"file"},
	}
}

func (t *PreviewDocumentTool) Scope() ToolScope { return ScopeGeneral }

func (t *PreviewDocumentTool) Category() ToolCategory { return CategoryFilesystem }

// errPreviewDocumentNotAvailable is the refusal every call receives until the
// preview pipeline lands.
const errPreviewDocumentNotAvailable = "preview_document: document preview is not available in this build yet"

func (t *PreviewDocumentTool) Execute(_ context.Context, _ map[string]any) *ToolResult {
	return ErrorResult(errPreviewDocumentNotAvailable)
}
