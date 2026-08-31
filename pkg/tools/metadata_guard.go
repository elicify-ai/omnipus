// Package tools — metadata path guard.
//
// Implements the fail-closed guard for agents/<id>/(SOUL|HEARTBEAT|MEMORY|AGENT).md:
// any generic file tool (read_file, write_file, edit_file, append_file) that
// resolves to one of these paths is rejected with a structured error that
// suggests update_agent (write) / read_agent_metadata (read) instead.
//
// The guard is case-insensitive over the four canonical names.
//
// Security property: the guard runs BEFORE ResolvePath — it resolves the
// tool's path argument itself (via resolveAbsPath, which applies
// filepath.EvalSymlinks best-effort) so that "SOUL.md", "./soul.md", an
// absolute path, a "../"-reentrant path, and a symlink to SOUL.md all trigger
// the same check. In sandbox-on (os.Root) mode the symlink vector is already
// defeated by the kernel; this guard additionally hardens god-mode.

package tools

import (
	"encoding/json"
	"path/filepath"
	"strings"
)

// canonicalMetadataNames maps the canonical key (lowercase) to the on-disk
// capitalised filename.  These are the four files that must only be read
// through read_agent_metadata, or written through update_agent.
//
// This is the single source of truth for the metadata-guard / metadata-tool
// security pair; pkg/sysagent/tools/metadata.go consumes it via the exported
// CanonicalMetadataFilename function below.
var canonicalMetadataNames = map[string]string{
	"soul":      "SOUL.md",
	"heartbeat": "HEARTBEAT.md",
	"memory":    "MEMORY.md",
	"agent":     "AGENT.md",
}

// CanonicalMetadataFilename returns the on-disk filename for a metadata key
// (soul/heartbeat/memory/agent). Returns ("", false) for unknown keys. The key
// match is case-insensitive. This is the exported single source of truth shared
// by the metadata guard (pkg/tools) and the metadata tools (pkg/sysagent/tools).
func CanonicalMetadataFilename(key string) (string, bool) {
	name, ok := canonicalMetadataNames[strings.ToLower(strings.TrimSpace(key))]
	return name, ok
}

// metadataFileMatch reports whether absPath is one of the four canonical
// metadata files inside an agents/<id>/ directory tree.
//
// Returns:
//   - fileKey  — canonical key (soul/heartbeat/memory/agent), or ""
//   - agentID  — the agent ID segment from the path, or ""
//   - ok       — true when the path matched
func metadataFileMatch(absPath string) (fileKey, agentID string, ok bool) {
	cleaned := filepath.Clean(absPath)

	// We need at least .../agents/<id>/<name>.md — three path components after
	// splitting from the right.
	dir := filepath.Dir(cleaned)              // .../agents/<id>
	base := filepath.Base(cleaned)            // <name>.md
	agentsDir := filepath.Dir(dir)            // .../agents
	agentsDirBase := filepath.Base(agentsDir) // "agents"

	if !strings.EqualFold(agentsDirBase, "agents") {
		return "", "", false
	}

	id := filepath.Base(dir)
	if id == "" || id == "." || id == "/" || id == agentsDirBase {
		return "", "", false
	}

	// Case-insensitive match against the four canonical filenames.
	baseLower := strings.ToLower(base)
	for key, canonical := range canonicalMetadataNames {
		if strings.ToLower(canonical) == baseLower {
			return key, id, true
		}
	}

	return "", "", false
}

// MetadataGuardNotice is the shared, single-source-of-truth explanation of the
// metadata guard's policy: which tool to use instead of a generic file tool
// for agents/<id>/(SOUL|HEARTBEAT|MEMORY|AGENT).md. Referenced by the
// write_file, edit_file, and append_file tool descriptions
// (pkg/tools/filesystem.go, pkg/tools/edit.go) and by metadataGuardError
// below, so a future change to this policy (e.g. a renamed replacement tool)
// only needs one edit.
const MetadataGuardNotice = "Agent metadata files (SOUL.md, HEARTBEAT.md, AGENT.md, MEMORY.md under agents/<id>/) are off-limits to generic file tools — use update_agent to write them, or read_agent_metadata to read them."

// updateAgentFieldForMetadataKey maps a metadata key to the update_agent
// parameter that writes it, for the keys update_agent has a direct field for
// (soul, heartbeat — see pkg/sysagent/tools/agent.go's AgentUpdateTool.
// Parameters()). "memory" (written via the remember tool) and "agent" (raw
// AGENT.md frontmatter, redundant with update_agent's structured fields) have
// no direct field and fall back to a generic update_agent(...) pointer.
var updateAgentFieldForMetadataKey = map[string]string{
	"soul":      "soul",
	"heartbeat": "heartbeat",
}

// metadataGuardError returns a structured JSON error string that tells the
// agent to use read_agent_metadata (read) / update_agent (write) instead.
//
// op should be "read" or "write". absPath is the resolved absolute path that
// was blocked. Callers always confirm a metadataFileMatch before invoking this
// (or fail closed via guardMetadataPath), so the match is expected to succeed;
// if it does not, the message degrades gracefully to the raw basename.
func metadataGuardError(absPath, op string) string {
	fileKey, agentID, _ := metadataFileMatch(absPath)
	if agentID == "" {
		agentID = "(unknown)"
	}

	var suggestion string
	if op == "read" {
		suggestion = `use read_agent_metadata(file="` + fileKey + `")`
	} else if field, ok := updateAgentFieldForMetadataKey[fileKey]; ok {
		suggestion = `use update_agent(id="` + agentID + `", ` + field + `=...)`
	} else {
		suggestion = `use update_agent(id="` + agentID + `", ...)`
	}

	canonical := canonicalMetadataNames[fileKey]
	if canonical == "" {
		canonical = filepath.Base(absPath)
	}

	msg := map[string]any{
		"error": map[string]any{
			"code":       "USE_METADATA_TOOL",
			"message":    "agents/" + agentID + "/" + canonical + " is managed by agent metadata tools. " + MetadataGuardNotice,
			"suggestion": suggestion,
		},
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return `{"error":{"code":"USE_METADATA_TOOL","message":"metadata file is managed by agent metadata tools","suggestion":"` + suggestion + `"}}`
	}
	return string(b)
}
