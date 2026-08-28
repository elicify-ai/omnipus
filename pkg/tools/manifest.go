// Omnipus — Tool manifest optimization (v0.1.0)
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package tools — manifest classification and compressed-manifest builder.
//
// The manifest optimization cuts per-turn token cost by sending only the
// high-frequency "full" tools as callable defs; all other allowed tools appear
// in a compact text block in the system context and are loaded on demand via
// the `ToolSearch` infra tool. See
// docs/internal/design/tool-manifest-optimization-2026-06.md and
// docs/internal/design/unified-tools-tool-2026-06.md.

package tools

import (
	"fmt"
	"sort"
	"strings"
)

// ManifestTier classifies how a tool is presented to the LLM when the manifest
// optimization is active (cfg.Tools.Manifest.Compressed == true).
type ManifestTier int

const (
	// ManifestFull — high-frequency tools always sent as callable defs every turn.
	ManifestFull ManifestTier = iota
	// ManifestLazy — all other allowed tools; appear in the compact manifest block
	// and are made callable on demand via the `ToolSearch` infra tool.
	ManifestLazy
	// ManifestInfra — infrastructure tools (the `ToolSearch` infra tool) that are
	// always callable when registered but never appear in the manifest block itself.
	ManifestInfra
)

// fullManifestToolNames is the set of high-frequency tools that are always sent
// as full callable defs (ManifestFull). These are the tools the agent reaches
// for in every or nearly-every turn. The list is intentionally small so the
// compressed surface contains the long tail.
var fullManifestToolNames = map[string]struct{}{
	"read_file":         {},
	"write_file":        {},
	"edit_file":         {},
	"list_directory":    {},
	"bash":              {}, // ADR-036: exec/workspace_shell/workspace_shell_bg merged into "bash".
	"search_web":        {},
	"fetch_url":         {},
	"send_message":      {},
	"hand_off":          {},
	"return_to_default": {},
	"remember":          {},
	"recall_memory":     {},
	"set_todos":         {},
	"navigate":          {},
	"create_task":       {},
	"list_tasks":        {},
	"update_task":       {},
	"delegate":          {}, // ADR-053: must be as visible as create_task/list_tasks/update_task,
	// otherwise the model reaches for the task route because it is the only
	// one it can see as a callable def (measured 304s vs 20-80s for delegate).
}

// infraManifestToolNames is the set of infrastructure tools that are always
// callable when registered but must never appear in the manifest block (they
// exist to drive the manifest mechanism itself).
var infraManifestToolNames = map[string]struct{}{
	"ToolSearch": {},
}

// ToolManifestTier returns the ManifestTier for the given tool name. This
// function is the single classification authority used by the agent loop's
// turn-build logic and by the manifest builder.
//
// The function is named ToolManifestTier (not ManifestTier) to avoid a
// collision with the ManifestTier type declared in this file.
func ToolManifestTier(name string) ManifestTier {
	if _, ok := infraManifestToolNames[name]; ok {
		return ManifestInfra
	}
	if _, ok := fullManifestToolNames[name]; ok {
		return ManifestFull
	}
	return ManifestLazy
}

// IsFullManifestTool reports whether name belongs to the full (always-callable)
// set. Exported for use by the agent loop and tests.
func IsFullManifestTool(name string) bool {
	_, ok := fullManifestToolNames[name]
	return ok
}

// FullManifestToolNames returns a sorted copy of the full-manifest tool name
// set. Exported for tests and tooling that needs to enumerate the set.
func FullManifestToolNames() []string {
	names := make([]string, 0, len(fullManifestToolNames))
	for n := range fullManifestToolNames {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// InfraManifestToolNames returns a sorted copy of the infrastructure tool name
// set (currently just "ToolSearch"). These are always callable when registered and
// never appear in the manifest block. Exported as the single source of truth so
// the agent loop's force-include logic does not re-list the names.
func InfraManifestToolNames() []string {
	names := make([]string, 0, len(infraManifestToolNames))
	for n := range infraManifestToolNames {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// maxManifestLineLen is the soft cap on each entry's description text
// (not including the "  - <name> — " prefix). Keeps the manifest cheap.
const maxManifestLineLen = 140

// BuildCompressedManifest renders the lazy (not-yet-loaded) tools as a compact
// manifest block suitable for injection into the system context. It groups
// tools by Category(), one line per tool "  - <name> — <first line of
// Description()>". Returns "" if there are no lazy unloaded tools so the
// caller can skip the injection entirely.
//
// Parameters:
//
//	lazyTools — the tools the agent has available (full+infra names are excluded
//	            by the tier check so callers may pass the full policy-filtered
//	            slice and this function will filter appropriately).
//	loaded    — names already loaded this session (excluded from the manifest).
//
// Output is deterministic: categories and tool names are sorted.
func BuildCompressedManifest(lazyTools []Tool, loaded map[string]bool) string {
	// Group tools by category, skipping full/infra/loaded.
	type entry struct {
		name string
		desc string
	}
	grouped := make(map[ToolCategory][]entry)
	for _, t := range lazyTools {
		n := t.Name()
		if ToolManifestTier(n) != ManifestLazy {
			continue
		}
		if loaded[n] {
			continue
		}
		// Truncate description to first line, then cap length.
		raw := t.Description()
		line := raw
		if idx := strings.IndexByte(raw, '\n'); idx >= 0 {
			line = raw[:idx]
		}
		line = strings.TrimSpace(line)
		if len(line) > maxManifestLineLen {
			line = line[:maxManifestLineLen]
		}
		cat := t.Category()
		grouped[cat] = append(grouped[cat], entry{name: n, desc: line})
	}

	if len(grouped) == 0 {
		return ""
	}

	// Sort categories for determinism.
	cats := make([]string, 0, len(grouped))
	for c := range grouped {
		cats = append(cats, string(c))
	}
	sort.Strings(cats)

	var sb strings.Builder
	sb.WriteString("# More tools (load before use)\n")
	sb.WriteString(
		"These tools are available but not loaded. To use one, call `ToolSearch` with its exact name in `names` (or describe what you need in `query`) to load it, then call it.\n",
	)

	for _, cat := range cats {
		entries := grouped[ToolCategory(cat)]
		// Sort entries within each category for determinism.
		sort.Slice(entries, func(i, j int) bool {
			return entries[i].name < entries[j].name
		})
		fmt.Fprintf(&sb, "\n## %s\n", cat)
		for _, e := range entries {
			fmt.Fprintf(&sb, "  - %s — %s\n", e.name, e.desc)
		}
	}

	return sb.String()
}
