package tools

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// EditFileTool edits a file by replacing old_text with new_text.
// The old_text must exist exactly in the file.
type EditFileTool struct {
	BaseTool
	agentHome string
	restrict  bool
	patterns  []*regexp.Regexp
}

// NewEditFileTool creates a new EditFileTool with optional directory restriction.
func NewEditFileTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *EditFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &EditFileTool{
		agentHome: workspace,
		restrict:  restrict,
		patterns:  patterns,
	}
}

func (t *EditFileTool) Name() string {
	return "edit_file"
}

func (t *EditFileTool) Description() string {
	return "Edit a file by replacing old_text with new_text. The old_text must exist exactly in the file."
}

func (t *EditFileTool) Scope() ToolScope       { return ScopeCore }
func (t *EditFileTool) Category() ToolCategory { return CategoryFilesystem }

func (t *EditFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to edit",
			},
			"old_text": map[string]any{
				"type":        "string",
				"description": "The exact text to find and replace",
			},
			"new_text": map[string]any{
				"type":        "string",
				"description": "The text to replace with",
			},
		},
		"required": []string{"path", "old_text", "new_text"},
	}
}

func (t *EditFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	policy, err := fspolicy.EffectiveFSPolicy(
		ctx, t.agentHome, TurnWorkspaceDir(ctx), t.restrict,
		config.OmnipusHomeDir(), ToolAgentID(ctx), ToolWorkspaceID(ctx),
	)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to resolve filesystem policy: %v", err))
	}

	// Metadata guard: reject edits to agents/<id>/(SOUL|HEARTBEAT|MEMORY|AGENT).md
	// via generic file tools — callers must use agent.write_metadata instead.
	if policy.WorkDir != "" {
		if denied := guardMetadataPath(policy.WorkDir, path, "write"); denied != nil {
			return denied
		}
	}

	oldText, ok := args["old_text"].(string)
	if !ok {
		return ErrorResult("old_text is required")
	}

	newText, ok := args["new_text"].(string)
	if !ok {
		return ErrorResult("new_text is required")
	}

	handle, err := ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpWrite, path, t.patterns)
	if err != nil {
		return PermissionDeniedResult(t.Name(), err, err.Error())
	}
	defer handle.Close()

	if err := editFile(handle, oldText, newText); err != nil {
		return ErrorResult(err.Error())
	}
	return SilentResult(fmt.Sprintf("File edited: %s", path))
}

type AppendFileTool struct {
	BaseTool
	agentHome string
	restrict  bool
	patterns  []*regexp.Regexp
}

func NewAppendFileTool(workspace string, restrict bool, allowPaths ...[]*regexp.Regexp) *AppendFileTool {
	var patterns []*regexp.Regexp
	if len(allowPaths) > 0 {
		patterns = allowPaths[0]
	}
	return &AppendFileTool{
		agentHome: workspace,
		restrict:  restrict,
		patterns:  patterns,
	}
}

func (t *AppendFileTool) Name() string {
	return "append_file"
}

func (t *AppendFileTool) Description() string {
	return "Append content to the end of a file"
}

func (t *AppendFileTool) Scope() ToolScope       { return ScopeCore }
func (t *AppendFileTool) Category() ToolCategory { return CategoryFilesystem }

func (t *AppendFileTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "The file path to append to",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "The content to append",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *AppendFileTool) Execute(ctx context.Context, args map[string]any) *ToolResult {
	path, ok := args["path"].(string)
	if !ok {
		return ErrorResult("path is required")
	}

	policy, err := fspolicy.EffectiveFSPolicy(
		ctx, t.agentHome, TurnWorkspaceDir(ctx), t.restrict,
		config.OmnipusHomeDir(), ToolAgentID(ctx), ToolWorkspaceID(ctx),
	)
	if err != nil {
		return ErrorResult(fmt.Sprintf("failed to resolve filesystem policy: %v", err))
	}

	// Metadata guard: reject appends to agents/<id>/(SOUL|HEARTBEAT|MEMORY|AGENT).md
	// via generic file tools — callers must use agent.write_metadata instead.
	if policy.WorkDir != "" {
		if denied := guardMetadataPath(policy.WorkDir, path, "write"); denied != nil {
			return denied
		}
	}

	content, ok := args["content"].(string)
	if !ok {
		return ErrorResult("content is required")
	}

	handle, err := ResolvePathAllowingPatterns(ctx, policy, t.Name(), "", FSOpWrite, path, t.patterns)
	if err != nil {
		return PermissionDeniedResult(t.Name(), err, err.Error())
	}
	defer handle.Close()

	if err := appendFile(handle, content); err != nil {
		return ErrorResult(err.Error())
	}
	return SilentResult(fmt.Sprintf("Appended to %s", path))
}

// editFile reads the file via handle, performs the replacement, and writes
// back through the same handle.
func editFile(handle *PathHandle, oldText, newText string) error {
	content, err := handle.ReadFile()
	if err != nil {
		return err
	}

	newContent, err := replaceEditContent(content, oldText, newText)
	if err != nil {
		return err
	}

	return handle.WriteFile(newContent)
}

// appendFile reads the existing content (if any) via handle, appends new
// content, and writes back through the same handle.
func appendFile(handle *PathHandle, appendContent string) error {
	content, err := handle.ReadFile()
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}

	newContent := append(content, []byte(appendContent)...)
	return handle.WriteFile(newContent)
}

// replaceEditContent handles the core logic of finding and replacing a single occurrence of oldText.
func replaceEditContent(content []byte, oldText, newText string) ([]byte, error) {
	contentStr := string(content)

	if !strings.Contains(contentStr, oldText) {
		return nil, fmt.Errorf("old_text not found in file. Make sure it matches exactly")
	}

	count := strings.Count(contentStr, oldText)
	if count > 1 {
		return nil, fmt.Errorf("old_text appears %d times. Please provide more context to make it unique", count)
	}

	newContent := strings.Replace(contentStr, oldText, newText, 1)
	return []byte(newContent), nil
}
