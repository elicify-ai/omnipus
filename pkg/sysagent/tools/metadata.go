// Omnipus — agent.read_metadata and agent.write_metadata tools.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// These are the deterministic, self-validating accessors for an agent's
// metadata files (SOUL.md, HEARTBEAT.md, MEMORY.md, AGENT.md). All other
// write paths for these files are blocked by the metadata guard in
// pkg/tools/metadata_guard.go and pkg/tools/filesystem.go.
//
// Design invariants:
//   - Path resolution uses deps.Home + canonical filename; validateID guards
//     path traversal.
//   - Writes are atomic via fileutil.WriteFileAtomic with 0o644 perms,
//     matching system.agent.create/update.
//   - AGENT.md writes are validated: the content must contain a valid
//     YAML frontmatter block (or no frontmatter at all — blank is valid).
//     Malformed frontmatter (e.g. unclosed ---) is rejected before any I/O.
//   - After a successful write deps.ReloadFunc() is called so the change is
//     live, matching create/update behavior.

package systools

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/dapicom-ai/omnipus/pkg/datamodel"
	"github.com/dapicom-ai/omnipus/pkg/fileutil"
	"github.com/dapicom-ai/omnipus/pkg/tools"
)

// canonicalMetadataFilename returns the on-disk filename for a metadata key
// (soul/heartbeat/memory/agent). Returns ("", false) for unknown keys.
func canonicalMetadataFilename(key string) (string, bool) {
	switch strings.ToLower(key) {
	case "soul":
		return "SOUL.md", true
	case "heartbeat":
		return "HEARTBEAT.md", true
	case "memory":
		return "MEMORY.md", true
	case "agent":
		return "AGENT.md", true
	default:
		return "", false
	}
}

// validateAgentMDFrontmatter parses the frontmatter block (if any) from content
// and returns an error when it is malformed YAML.  A file with no frontmatter
// (no leading --- block) is always valid.
func validateAgentMDFrontmatter(content string) error {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || lines[0] != "---" {
		// No frontmatter — valid.
		return nil
	}

	// Find the closing ---.
	end := -1
	for i := 1; i < len(lines); i++ {
		if lines[i] == "---" {
			end = i
			break
		}
	}
	if end == -1 {
		return fmt.Errorf("AGENT.md frontmatter block is not closed: missing closing '---'")
	}

	fmContent := strings.Join(lines[1:end], "\n")
	var rawFields map[string]any
	if err := yaml.Unmarshal([]byte(fmContent), &rawFields); err != nil {
		return fmt.Errorf("AGENT.md frontmatter is not valid YAML: %w", err)
	}
	return nil
}

// ---- agent.read_metadata ----

// AgentReadMetadataTool implements agent.read_metadata.
type AgentReadMetadataTool struct{ deps *Deps }

func NewAgentReadMetadataTool(d *Deps) *AgentReadMetadataTool {
	return &AgentReadMetadataTool{deps: d}
}

func (t *AgentReadMetadataTool) Name() string           { return "system.agent.read_metadata" }
func (t *AgentReadMetadataTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentReadMetadataTool) Description() string {
	return "Read one of the calling agent's metadata files (SOUL.md, HEARTBEAT.md, MEMORY.md, or AGENT.md). Pass agent_id to read another agent's metadata (subject to per-agent policy)."
}

func (t *AgentReadMetadataTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file": map[string]any{
				"type":        "string",
				"enum":        []string{"soul", "heartbeat", "memory", "agent"},
				"description": "Which metadata file to read: soul, heartbeat, memory, or agent.",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "Agent ID whose metadata to read. Defaults to the calling agent's own ID.",
			},
		},
		"required": []string{"file"},
	}
}

func (t *AgentReadMetadataTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	fileKey, _ := args["file"].(string)
	fileKey = strings.ToLower(strings.TrimSpace(fileKey))
	filename, ok := canonicalMetadataFilename(fileKey)
	if !ok {
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("unknown file %q: must be one of soul, heartbeat, memory, agent", fileKey),
			"Pass file=soul, file=heartbeat, file=memory, or file=agent"))
	}

	// Resolve agent ID: explicit arg or fall back to calling agent.
	agentID, _ := args["agent_id"].(string)
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = tools.ToolAgentID(ctx)
	}
	if agentID == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			"agent_id is required when the calling agent ID is not available",
			"Pass agent_id explicitly"))
	}
	if err := validateID(agentID); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}

	wsPath := datamodel.AgentWorkspacePath(t.deps.Home, agentID)
	filePath := wsPath + "/" + filename

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return tools.ErrorResult(errorJSON("NOT_FOUND",
				fmt.Sprintf("%s does not exist for agent %q", filename, agentID),
				"The file has not been written yet. Use agent.write_metadata to create it."))
		}
		return tools.ErrorResult(errorJSON("READ_ERROR",
			fmt.Sprintf("could not read %s: %v", filename, err),
			"Check disk space and permissions"))
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"agent_id": agentID,
		"file":     fileKey,
		"content":  string(data),
	}))
}

// ---- agent.write_metadata ----

// AgentWriteMetadataTool implements agent.write_metadata.
type AgentWriteMetadataTool struct{ deps *Deps }

func NewAgentWriteMetadataTool(d *Deps) *AgentWriteMetadataTool {
	return &AgentWriteMetadataTool{deps: d}
}

func (t *AgentWriteMetadataTool) Name() string           { return "system.agent.write_metadata" }
func (t *AgentWriteMetadataTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentWriteMetadataTool) Description() string {
	return "Write one of the calling agent's metadata files (SOUL.md, HEARTBEAT.md, MEMORY.md, or AGENT.md) atomically. AGENT.md content is validated for well-formed frontmatter before writing. Pass agent_id to write another agent's metadata (subject to per-agent policy)."
}

func (t *AgentWriteMetadataTool) Parameters() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"file": map[string]any{
				"type":        "string",
				"enum":        []string{"soul", "heartbeat", "memory", "agent"},
				"description": "Which metadata file to write: soul, heartbeat, memory, or agent.",
			},
			"content": map[string]any{
				"type":        "string",
				"description": "New content for the file. For AGENT.md, any frontmatter block must be valid YAML.",
			},
			"agent_id": map[string]any{
				"type":        "string",
				"description": "Agent ID to write metadata for. Defaults to the calling agent's own ID.",
			},
		},
		"required": []string{"file", "content"},
	}
}

func (t *AgentWriteMetadataTool) Execute(ctx context.Context, args map[string]any) *tools.ToolResult {
	fileKey, _ := args["file"].(string)
	fileKey = strings.ToLower(strings.TrimSpace(fileKey))
	filename, ok := canonicalMetadataFilename(fileKey)
	if !ok {
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			fmt.Sprintf("unknown file %q: must be one of soul, heartbeat, memory, agent", fileKey),
			"Pass file=soul, file=heartbeat, file=memory, or file=agent"))
	}

	content, ok := args["content"].(string)
	if !ok {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", "content is required", ""))
	}

	// Resolve agent ID.
	agentID, _ := args["agent_id"].(string)
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		agentID = tools.ToolAgentID(ctx)
	}
	if agentID == "" {
		return tools.ErrorResult(errorJSON("INVALID_INPUT",
			"agent_id is required when the calling agent ID is not available",
			"Pass agent_id explicitly"))
	}
	if err := validateID(agentID); err != nil {
		return tools.ErrorResult(errorJSON("INVALID_INPUT", err.Error(), ""))
	}

	// Self-validating: for AGENT.md, validate frontmatter before any I/O.
	if fileKey == "agent" {
		if err := validateAgentMDFrontmatter(content); err != nil {
			return tools.ErrorResult(errorJSON("INVALID_FRONTMATTER", err.Error(),
				"Ensure the --- ... --- block contains valid YAML, or omit the frontmatter block entirely"))
		}
	}

	wsPath := datamodel.AgentWorkspacePath(t.deps.Home, agentID)
	filePath := wsPath + "/" + filename

	// Ensure workspace directory exists (agent may not have been initialized yet).
	if err := os.MkdirAll(wsPath, 0o700); err != nil {
		return tools.ErrorResult(errorJSON("WORKSPACE_ERROR",
			fmt.Sprintf("could not create agent workspace: %v", err),
			"Check disk space and permissions"))
	}

	// Atomic write — same permissions used by system.agent.create/update.
	if err := fileutil.WriteFileAtomic(filePath, []byte(content), 0o644); err != nil {
		return tools.ErrorResult(errorJSON("WRITE_ERROR",
			fmt.Sprintf("could not write %s: %v", filename, err),
			"Check disk space and permissions"))
	}

	// Hot-reload so the change is immediately visible to the agent loop.
	if t.deps.ReloadFunc != nil {
		if err := t.deps.ReloadFunc(); err != nil {
			slog.Warn("agent.write_metadata: hot-reload failed — change will be visible after restart",
				"agent_id", agentID, "file", filename, "error", err)
		}
	}

	return tools.NewToolResult(successJSON(map[string]any{
		"agent_id": agentID,
		"file":     fileKey,
		"written":  true,
	}))
}
