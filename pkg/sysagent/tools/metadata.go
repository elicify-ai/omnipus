// Omnipus — agent.read_metadata tool.
// License: MIT
// Copyright (c) 2026 Omnipus contributors
//
// This is the deterministic, self-validating read accessor for an agent's
// metadata files (SOUL.md, HEARTBEAT.md, MEMORY.md, AGENT.md). The four generic
// file tools (read_file, write_file, edit_file, append_file) are blocked from
// touching these files by the metadata guard in pkg/tools/metadata_guard.go and
// pkg/tools/filesystem.go; other write paths (e.g. system.agent.create/update,
// the agent loader) are not routed through the guard.
//
// Its write counterpart, agent.write_metadata, was retired (tool-manifest-
// tier-redesign review F6): it was a redundant, unguarded second door onto the
// same files update_agent already writes through a properly-guarded path
// (update_agent refuses locked core agents; write_agent_metadata had no such
// check, so it could silently overwrite a locked core agent's SOUL.md).
// MEMORY.md has its own proper writer (the remember tool); raw AGENT.md
// frontmatter editing is redundant with update_agent's structured, validated
// fields.
//
// Design invariants:
//   - Path resolution uses deps.Home + canonical filename; validateID guards
//     path traversal.
//   - The canonical key→filename mapping is the single source of truth in
//     pkg/tools (tools.CanonicalMetadataFilename), shared with the guard.

package systools

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/elicify-ai/omnipus/pkg/datamodel"
	"github.com/elicify-ai/omnipus/pkg/tools"
)

// ---- agent.read_metadata ----

// AgentReadMetadataTool implements agent.read_metadata.
type AgentReadMetadataTool struct{ deps *Deps }

func NewAgentReadMetadataTool(d *Deps) *AgentReadMetadataTool {
	return &AgentReadMetadataTool{deps: d}
}

func (t *AgentReadMetadataTool) Name() string           { return "read_agent_metadata" }
func (t *AgentReadMetadataTool) Scope() tools.ToolScope { return tools.ScopeCore }
func (t *AgentReadMetadataTool) Description() string {
	return "Read one of an agent's metadata files: SOUL.md (personality, persona, system prompt and behavioural instructions), HEARTBEAT.md (proactive schedule), MEMORY.md (procedural/episodic memory), or AGENT.md (frontmatter config). Defaults to the calling agent's own files; pass agent_id to read another agent's. There is no per-target permission check — any agent granted this tool can read any agent's metadata, including MEMORY.md."
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
	filename, ok := tools.CanonicalMetadataFilename(fileKey)
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

	wsPath := datamodel.AgentHomePath(t.deps.Home, agentID)
	filePath := wsPath + "/" + filename

	data, err := os.ReadFile(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return tools.ErrorResult(errorJSON("NOT_FOUND",
				fmt.Sprintf("%s does not exist for agent %q", filename, agentID),
				"The file has not been written yet."))
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
