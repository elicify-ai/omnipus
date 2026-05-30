// Omnipus — Central Builtin Tool Registry
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — BuiltinRegistry is the central registry for all builtin
// tools. There is exactly one BuiltinRegistry for the process lifetime (FR-001,
// FR-002). All builtin tools are registered here once at boot before any agent
// or MCP server is started.
//
// Design invariants (binding):
//   - Tools are registered exactly once at boot. RegisterBuiltin returns an error on
//     duplicate names — the caller should log it and abort boot on the first duplicate.
//   - The BuiltinRegistry is read-only after boot population completes (no runtime
//     registration). Per-agent policy and MCP dynamic tools are layered on top.
//   - The registry is safe for concurrent reads from multiple goroutines (RWMutex).
//   - Source tag for every entry is "builtin" (distinguishes from "mcp:serverName").
//   - MCP registrations whose name begins with "system." are rejected here
//     (FR-060) via the RejectsMCPSystemPrefix check.

package tools

import (
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
)

// BuiltinEntry describes a single builtin tool as returned by Describe.
type BuiltinEntry struct {
	Name        string       `json:"name"`
	Description string       `json:"description"`
	Scope       ToolScope    `json:"scope"`
	Category    ToolCategory `json:"category"`
	Source      string       `json:"source"` // always "builtin"
}

// BuiltinRegistry is the single central registry for all builtin tools.
// There is one per process, populated synchronously at boot before MCP or
// agents are started (Boot Order step 2 per the tool-registry-redesign spec).
type BuiltinRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool // name → tool
}

// NewBuiltinRegistry creates an empty BuiltinRegistry.
// Tools are registered via RegisterBuiltin.
func NewBuiltinRegistry() *BuiltinRegistry {
	return &BuiltinRegistry{
		tools: make(map[string]Tool),
	}
}

// RegisterBuiltin adds a builtin tool to the registry.
// Returns an error if the name is empty or already registered.
// Callers must treat a non-nil error as a boot-time fatal: duplicate registrations
// indicate a programmer error and must not be silently swallowed.
func (r *BuiltinRegistry) RegisterBuiltin(t Tool) error {
	if t == nil {
		return fmt.Errorf("tools.BuiltinRegistry: cannot register nil tool")
	}
	name := t.Name()
	if name == "" {
		return fmt.Errorf("tools.BuiltinRegistry: cannot register tool with empty name")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if existing, ok := r.tools[name]; ok {
		return fmt.Errorf("tools.BuiltinRegistry: duplicate registration for %q (existing: %T)", name, existing)
	}
	r.tools[name] = t
	slog.Debug("tools.BuiltinRegistry: registered builtin", "name", name)
	return nil
}

// MustRegisterBuiltin calls RegisterBuiltin and panics on error.
// Use only during boot where a duplicate registration is a programmer error.
func (r *BuiltinRegistry) MustRegisterBuiltin(t Tool) {
	if err := r.RegisterBuiltin(t); err != nil {
		panic(err)
	}
}

// Get returns the builtin tool registered under name, or (nil, false).
func (r *BuiltinRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// All returns all registered builtin tools in sorted name order.
// The slice is a snapshot; subsequent registrations are not reflected.
func (r *BuiltinRegistry) All() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]Tool, 0, len(names))
	for _, n := range names {
		out = append(out, r.tools[n])
	}
	return out
}

// Describe returns a snapshot of all entries as BuiltinEntry structs for
// REST and catalog endpoints. Always sorted by name (deterministic).
func (r *BuiltinRegistry) Describe() []BuiltinEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.tools))
	for n := range r.tools {
		names = append(names, n)
	}
	sort.Strings(names)
	out := make([]BuiltinEntry, 0, len(names))
	for _, n := range names {
		t := r.tools[n]
		out = append(out, BuiltinEntry{
			Name:        n,
			Description: t.Description(),
			Scope:       t.Scope(),
			Category:    t.Category(),
			Source:      "builtin",
		})
	}
	return out
}

// Count returns the number of registered builtins.
func (r *BuiltinRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// ValidateMCPName returns an error if the given MCP tool name would conflict
// with the builtin registry (FR-034, FR-060).
//   - If the name begins with "system." it is rejected unconditionally
//     (`conflict_with: "reserved_prefix"`) per FR-060.
//   - If the name matches an existing builtin, it is rejected
//     (`conflict_with: "builtin"`) per FR-034.
func (r *BuiltinRegistry) ValidateMCPName(name string) error {
	if strings.HasPrefix(name, "system.") {
		return fmt.Errorf(
			"tools.BuiltinRegistry: MCP tool %q rejected: name begins with reserved prefix \"system.\"",
			name,
		)
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tools.BuiltinRegistry: MCP tool %q rejected: conflicts with registered builtin", name)
	}
	return nil
}
