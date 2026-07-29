// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// library_list / library_read (D3, library-spec).
//
// A 2026-07-29 UAT session exposed a real failure: a user uploaded a file
// via chat, the agent correctly reported it could not find it by guessing a
// filename, because NOTHING in the tool surface could enumerate or read
// what chat uploads actually land as (pkg/gateway's HandleUpload dual-writes
// them into workspaces/<id>/work/.library/<filename> — see
// pkg/agent.LibraryDirName / RecordUploadWorkPath). These two tools close
// that gap: a thin, purpose-built facade over the SAME ListDirTool/
// ReadFileTool machinery every other file tool uses, scoped to the
// workspace's own .library/ directory.
//
// Both tools are thin wrappers, not reimplementations: they only rewrite
// the caller's path argument to always resolve inside .library/, then
// delegate to an embedded ListDirTool/ReadFileTool. This guarantees
// identical behavior to the generic tools for everything that isn't
// path-scoping — binary detection, document extraction, pagination, the
// os.Root confinement, and (critically) NOT filtering dot-entries out of a
// directory listing. formatDirEntries (filesystem.go) lists every
// os.DirEntry unconditionally; there is no dotfile-hiding anywhere in this
// call chain to trip over.

package tools

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// libraryDirName is the workspace work-tree subdirectory chat uploads are
// dual-written into (D-1, library-spec). This is an INDEPENDENT literal,
// duplicated rather than imported from pkg/agent.LibraryDirName(): pkg/agent
// already imports pkg/tools (AgentInstance.Tools is a *tools.ToolRegistry),
// so the reverse import would cycle. This mirrors the same
// duplicate-the-check-instead-of-importing-the-neighbor posture
// pkg/media/library/library.go's safeWorkspaceDir already uses for
// pkg/workspace's safeID, for the identical reason.
const libraryDirName = ".library"

// joinLibraryPath validates a caller-supplied sub-path and joins it under
// libraryDirName. Rejects a path that would escape .library/ itself (a
// leading ".." or an absolute path) — even though the outer per-turn
// os.Root confinement (ADR-046, enforced by ResolvePath inside the wrapped
// ListDirTool/ReadFileTool) already prevents escaping the workspace's work/
// tree entirely, this keeps these two tools' OWN advertised scope
// ("the workspace library") meaningful rather than silently reinterpreting
// an escaping argument as "somewhere else in work/".
func joinLibraryPath(sub string) (string, error) {
	sub = strings.TrimSpace(sub)
	if sub == "" || sub == "." {
		return libraryDirName, nil
	}
	if filepath.IsAbs(sub) {
		return "", fmt.Errorf("path must be relative to the workspace library (.library/), not absolute")
	}
	cleaned := filepath.Clean(sub)
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("path must stay within the workspace library (.library/): %q escapes it", sub)
	}
	return filepath.Join(libraryDirName, cleaned), nil
}

// LibraryListTool lists files inside the calling agent's own workspace
// library (D3, library-spec). Delegates to ListDirTool so listing behavior
// (including dot-entry visibility) never drifts from the generic tool.
type LibraryListTool struct {
	inner *ListDirTool
}

// NewLibraryListTool constructs a LibraryListTool. Parameters mirror
// NewListDirTool exactly — same workspace/restrict/allowPaths shape every
// sibling file tool constructor uses.
func NewLibraryListTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *LibraryListTool {
	return &LibraryListTool{inner: NewListDirTool(workspace, restrict, allowPaths...)}
}

func (t *LibraryListTool) Name() string {
	return "library_list"
}

func (t *LibraryListTool) Description() string {
	return "List files in this workspace's library (chat file uploads land here — see library_read). " +
		"Optional \"path\" narrows to a subdirectory within the library; omit it to list the library root."
}

func (t *LibraryListTool) Scope() ToolScope       { return ScopeGeneral }
func (t *LibraryListTool) Category() ToolCategory { return CategoryFilesystem }

func (t *LibraryListTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Subdirectory within the library to list (optional; defaults to the library root).",
			},
		},
	}
}

func (t *LibraryListTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	rawPath, _ := args["path"].(string)
	joined, err := joinLibraryPath(rawPath)
	if err != nil {
		return ErrorResult(err.Error())
	}
	return t.inner.Execute(ctx, map[string]any{"path": joined})
}

// LibraryReadTool reads a file inside the calling agent's own workspace
// library (D3, library-spec). Delegates to ReadFileTool so read behavior
// (binary detection, document extraction, pagination, sandbox confinement)
// never drifts from the generic tool.
type LibraryReadTool struct {
	inner *ReadFileTool
}

// NewLibraryReadTool constructs a LibraryReadTool. Parameters mirror
// NewReadFileTool exactly — same workspace/restrict/maxReadFileSize/
// allowPaths shape every sibling file tool constructor uses.
func NewLibraryReadTool(
	workspace string,
	restrict bool,
	maxReadFileSize int,
	allowPaths ...[]*regexp.Regexp,
) *LibraryReadTool {
	return &LibraryReadTool{inner: NewReadFileTool(workspace, restrict, maxReadFileSize, allowPaths...)}
}

func (t *LibraryReadTool) Name() string {
	return "library_read"
}

func (t *LibraryReadTool) Description() string {
	return "Read a file from this workspace's library (chat file uploads land here). " +
		"Give the exact path announced when the file was uploaded (e.g. \"report.pptx\"), " +
		"or call library_list first if you are not sure of the name — never guess a filename."
}

func (t *LibraryReadTool) Scope() ToolScope       { return ScopeGeneral }
func (t *LibraryReadTool) Category() ToolCategory { return CategoryFilesystem }

func (t *LibraryReadTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "File path within the library, e.g. \"report.pptx\".",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Byte offset to start reading from (default 0).",
			},
			"length": map[string]any{
				"type":        "integer",
				"description": "Number of bytes to read (default/max the configured read limit).",
			},
		},
		"required": []string{"path"},
	}
}

func (t *LibraryReadTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	rawPath, ok := args["path"].(string)
	if !ok || strings.TrimSpace(rawPath) == "" {
		return ErrorResult("path is required")
	}
	joined, err := joinLibraryPath(rawPath)
	if err != nil {
		return ErrorResult(err.Error())
	}
	innerArgs := make(map[string]any, len(args))
	for k, v := range args {
		innerArgs[k] = v
	}
	innerArgs["path"] = joined
	return t.inner.Execute(ctx, innerArgs)
}

// SetAuditLogger forwards to the wrapped ReadFileTool so path.access_denied
// audit events on a workspace-guard rejection still fire for library_read,
// exactly as they do for read_file.
func (t *LibraryReadTool) SetAuditLogger(l *audit.Logger) {
	t.inner.SetAuditLogger(l)
}
