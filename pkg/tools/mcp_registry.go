// Omnipus — Central MCP Tool Registry
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — MCPRegistry is the central registry for tools provided by
// connected MCP servers (FR-001). Unlike the BuiltinRegistry, the MCPRegistry
// is dynamic: entries are added when MCP servers connect and removed when they
// disconnect. It is populated only after the BuiltinRegistry is fully populated
// (Boot Order step 7 per the tool-registry-redesign spec).
//
// Design invariants (binding):
//   - First-server-wins for collisions between MCP servers (FR-034). The second
//     registration of the same name from a different server is rejected and an
//     audit/warn path is triggered.
//   - Tools whose name begins with "system." are rejected (FR-060) — the
//     BuiltinRegistry.ValidateMCPName check is applied before admission.
//   - When a server disconnects, ALL entries for that server are evicted atomically
//     under a single lock acquisition (no torn state).
//   - Rename detection (FR-083): RegisterServerTools tracks each server by
//     (transportType, endpoint) fingerprint. When a known fingerprint reappears
//     under a new serverID, the old server is atomically evicted and the new
//     server is registered (rename). A "mcp.server.renamed" audit event is
//     emitted and per-agent policies referencing the old name are updated.
//   - The registry is safe for concurrent reads and writes (RWMutex). Per-LLM-call
//     filter snapshots via All() observe a consistent state.

package tools

import (
	"fmt"
	"log/slog"
	"sort"
	"sync"
)

// MCPEntry describes a single MCP tool as returned by Describe.
type MCPEntry struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Scope       ToolScope    `json:"scope"`
	Category    ToolCategory `json:"category"`
	Source      string       `json:"source"` // "mcp:<serverID>"
	ServerID    string       `json:"server_id"`
}

// mcpToolRecord holds a registered MCP tool plus its owning server.
type mcpToolRecord struct {
	serverID string
	tool     Tool
}

// mcpServerFingerprint uniquely identifies an MCP server by transport endpoint.
// Used for rename detection (FR-083).
type mcpServerFingerprint struct {
	TransportType string // "stdio", "sse", "http"
	Endpoint      string // URL (sse/http) or command path (stdio)
}

// mcpServerMeta holds metadata about a registered server.
type mcpServerMeta struct {
	fingerprint mcpServerFingerprint
}

// MCPPolicyUpdater is called after a server rename to update per-agent policies
// that referenced the old serverID. Implementations should replace occurrences
// of oldServerID with newServerID in agent tool policy maps.
// Passing nil is safe — policy update is skipped.
type MCPPolicyUpdater func(oldServerID, newServerID string)

// MCPRegistry is the dynamic central registry for MCP server tools.
// There is one per process. Entries are added and removed at runtime
// as servers connect and disconnect.
type MCPRegistry struct {
	mu            sync.RWMutex
	byName        map[string]*mcpToolRecord       // name → record (first-server-wins)
	byServer      map[string][]string             // serverID → []tool names (for fast eviction)
	serverMeta    map[string]*mcpServerMeta       // serverID → metadata
	byFingerprint map[mcpServerFingerprint]string // fingerprint → serverID
}

// NewMCPRegistry creates an empty MCPRegistry.
func NewMCPRegistry() *MCPRegistry {
	return &MCPRegistry{
		byName:        make(map[string]*mcpToolRecord),
		byServer:      make(map[string][]string),
		serverMeta:    make(map[string]*mcpServerMeta),
		byFingerprint: make(map[mcpServerFingerprint]string),
	}
}

// MCPServerOpts carries optional metadata for a server registration.
// Pass zero value (MCPServerOpts{}) when no metadata is available.
type MCPServerOpts struct {
	// TransportType is "stdio", "sse", or "http". Used for rename detection fingerprint.
	TransportType string
	// Endpoint is the URL (sse/http) or command path (stdio). Used for rename detection fingerprint.
	Endpoint string
	// RequiresAdminAsk lists tool names within this server that must have
	// RequiresAdminAsk() return true (FR-064). Names not in this list return false.
	RequiresAdminAsk []string
	// PolicyUpdater is called when a rename is detected to propagate the new
	// serverID into per-agent policy maps. Nil = skip policy update.
	PolicyUpdater MCPPolicyUpdater
}

// mcpAdminAskTool wraps a Tool and overrides RequiresAdminAsk() (FR-064).
type mcpAdminAskTool struct {
	Tool
	requiresAdminAsk bool
}

func (a *mcpAdminAskTool) RequiresAdminAsk() bool { return a.requiresAdminAsk }

// RegisterServerTools registers all tools from a single MCP server.
// Rules (FR-034, FR-060, FR-083):
//   - Tools whose name begins with "system." are rejected unconditionally.
//   - If a tool name is already registered by a different server, the new
//     entry is rejected (first-server-wins) and a warning is emitted.
//   - If the tool name is already registered by the SAME server, it is
//     replaced (reconnect case, idempotent).
//   - If opts.TransportType and opts.Endpoint identify a known fingerprint
//     under a different serverID, the old server is atomically evicted and
//     the new serverID takes over (rename detection, FR-083).
//   - opts.RequiresAdminAsk lists tool names that return true from
//     RequiresAdminAsk() (FR-064).
//
// Returns a slice of (name, reason) pairs for each rejected registration.
// The caller should emit audit events for each rejected entry.
func (r *MCPRegistry) RegisterServerTools(serverID string, tools []Tool, builtins *BuiltinRegistry) []MCPCollision {
	return r.RegisterServerToolsWithOpts(serverID, tools, builtins, MCPServerOpts{})
}

// RegisterServerToolsWithOpts is the extended form of RegisterServerTools.
// Use this when transport metadata (for rename detection) or per-tool
// RequiresAdminAsk overrides (FR-064) are available.
func (r *MCPRegistry) RegisterServerToolsWithOpts(
	serverID string,
	toolList []Tool,
	builtins *BuiltinRegistry,
	opts MCPServerOpts,
) []MCPCollision {
	r.mu.Lock()
	defer r.mu.Unlock()

	// --- FR-083: rename detection ---
	// Build fingerprint only when both fields are populated.
	fp := mcpServerFingerprint{
		TransportType: opts.TransportType,
		Endpoint:      opts.Endpoint,
	}
	if fp.TransportType != "" && fp.Endpoint != "" {
		if existingID, found := r.byFingerprint[fp]; found && existingID != serverID {
			// Same transport endpoint appeared under a new serverID → rename.
			slog.Info("tools.MCPRegistry: server rename detected — evicting old server",
				"old_server", existingID, "new_server", serverID,
				"transport", fp.TransportType, "endpoint", fp.Endpoint)

			// Atomically evict the old server's tools.
			r.evictServerLocked(existingID)

			// Emit audit (best-effort slog; callers may also emit via audit.Logger).
			slog.Info("tools.MCPRegistry: mcp.server.renamed",
				"event", "mcp.server.renamed",
				"old_server_id", existingID, "new_server_id", serverID)

			// Update per-agent policies that referenced the old name.
			if opts.PolicyUpdater != nil {
				opts.PolicyUpdater(existingID, serverID)
			}
		}
		// Record fingerprint → new serverID mapping.
		r.byFingerprint[fp] = serverID
	}

	// --- FR-064: build admin-ask lookup set ---
	adminAskSet := make(map[string]struct{}, len(opts.RequiresAdminAsk))
	for _, name := range opts.RequiresAdminAsk {
		adminAskSet[name] = struct{}{}
	}

	var collisions []MCPCollision
	var accepted []string

	for _, t := range toolList {
		name := t.Name()

		// FR-060: reject system.* prefix unconditionally.
		if builtins != nil {
			if err := builtins.ValidateMCPName(name); err != nil {
				collisions = append(collisions, MCPCollision{
					ToolName:     name,
					ServerID:     serverID,
					ConflictWith: "reserved_prefix",
					Reason:       err.Error(),
				})
				activeToolMetricsRecorder.IncCollisionTotal("reserved_prefix") // FR-039
				slog.Warn("tools.MCPRegistry: MCP tool rejected (reserved prefix)", "tool", name, "server", serverID)
				continue
			}
		}

		// FR-034: first-server-wins on name collision.
		if existing, exists := r.byName[name]; exists && existing.serverID != serverID {
			collisions = append(collisions, MCPCollision{
				ToolName:     name,
				ServerID:     serverID,
				ConflictWith: existing.serverID,
				Reason:       fmt.Sprintf("already registered by server %q", existing.serverID),
			})
			activeToolMetricsRecorder.IncCollisionTotal(existing.serverID) // FR-039
			slog.Warn("tools.MCPRegistry: MCP tool rejected (first-server-wins)",
				"tool", name, "new_server", serverID, "existing_server", existing.serverID)
			continue
		}

		// FR-064: wrap tool with admin-ask override if listed.
		registered := t
		if _, needsAsk := adminAskSet[name]; needsAsk {
			registered = &mcpAdminAskTool{Tool: t, requiresAdminAsk: true}
		}

		// Accept: overwrite if same server (reconnect), add if new.
		r.byName[name] = &mcpToolRecord{serverID: serverID, tool: registered}
		accepted = append(accepted, name)
	}

	// Replace the server's tool list atomically.
	// Remove any old names that are no longer in the new set.
	oldNames := r.byServer[serverID]
	newNameSet := make(map[string]struct{}, len(accepted))
	for _, n := range accepted {
		newNameSet[n] = struct{}{}
	}
	for _, old := range oldNames {
		if _, still := newNameSet[old]; !still {
			// Evict stale entry from this server.
			if rec, ok := r.byName[old]; ok && rec.serverID == serverID {
				delete(r.byName, old)
			}
		}
	}
	r.byServer[serverID] = accepted

	// Store server metadata.
	r.serverMeta[serverID] = &mcpServerMeta{fingerprint: fp}

	slog.Info("tools.MCPRegistry: server tools registered",
		"server", serverID, "accepted", len(accepted), "rejected", len(collisions))
	return collisions
}

// evictServerLocked removes all tools from serverID.
// Caller must hold r.mu write lock.
func (r *MCPRegistry) evictServerLocked(serverID string) {
	names := r.byServer[serverID]
	for _, name := range names {
		if rec, ok := r.byName[name]; ok && rec.serverID == serverID {
			delete(r.byName, name)
		}
	}
	delete(r.byServer, serverID)
	// Remove fingerprint mapping for this server.
	if meta, ok := r.serverMeta[serverID]; ok {
		fp := meta.fingerprint
		if fp.TransportType != "" && fp.Endpoint != "" {
			if r.byFingerprint[fp] == serverID {
				delete(r.byFingerprint, fp)
			}
		}
		delete(r.serverMeta, serverID)
	}
	slog.Info("tools.MCPRegistry: server evicted (locked)", "server", serverID, "tools_removed", len(names))
}

// EvictServer removes all tools from the named server atomically.
func (r *MCPRegistry) EvictServer(serverID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	names := r.byServer[serverID]
	r.evictServerLocked(serverID)
	slog.Info("tools.MCPRegistry: server evicted", "server", serverID, "tools_removed", len(names))
}

// Get returns the MCP tool registered under name, or (nil, false).
func (r *MCPRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	rec, ok := r.byName[name]
	if !ok {
		return nil, false
	}
	return rec.tool, true
}

// All returns all accepted MCP tools in sorted name order (snapshot).
func (r *MCPRegistry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		out = append(out, r.byName[n].tool)
	}
	return out
}

// Describe returns a snapshot of all MCP entries as MCPEntry structs.
func (r *MCPRegistry) Describe() []MCPEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.byName))
	for n := range r.byName {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]MCPEntry, 0, len(names))
	for _, n := range names {
		rec := r.byName[n]
		t := rec.tool
		out = append(out, MCPEntry{
			Name:        n,
			Description: t.Description(),
			Scope:       t.Scope(),
			Category:    t.Category(),
			Source:      "mcp:" + rec.serverID,
			ServerID:    rec.serverID,
		})
	}
	return out
}

// Count returns the number of accepted MCP tools.
func (r *MCPRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.byName)
}

// MCPCollision records a rejected MCP tool registration.
type MCPCollision struct {
	ToolName     string
	ServerID     string
	ConflictWith string // "builtin", "reserved_prefix", or another server ID
	Reason       string
}
