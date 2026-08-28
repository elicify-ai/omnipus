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
	"sync/atomic"
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
//
// ADR-071 D3 §4.1 (17 names, "as of 2026-08-27" against the 89-name post-D4
// catalog): `bash`, `navigate`, `create_task`, `update_task` moved OUT to the
// new previewed tier (Tier 2, see previewedLazyToolNames below) — removing
// bash's permanent visibility advantage over narrower purpose-built tools,
// and demoting the two task-mutation verbs while `delegate`/`list_tasks`
// stay Full so ADR-053's ordering (delegate must be at least as visible as
// the task tools) holds with a wider margin. `list_mounts`, `send_file`,
// `message_parent`, `recall_conversation` moved IN — conversational-control
// and addressing primitives with no natural discovery moment, and (for
// recall_conversation) the agent's only route back to history evicted by
// ADR-028's windowTrim, where an invisible recall tool reads to the user as
// the agent having forgotten. Membership is pinned by TestVisibility_TierArithmetic
// — do not hand-edit this map without updating that test's literal list too.
var fullManifestToolNames = map[string]struct{}{
	"read_file":           {},
	"write_file":          {},
	"edit_file":           {},
	"list_directory":      {},
	"list_mounts":         {}, // ADR-071 D3: promoted from lazy — addressing primitive, no discovery moment.
	"search_web":          {},
	"fetch_url":           {},
	"send_message":        {},
	"switch_agent":        {}, // ADR-071 D4: hand_off/return_to_default merged into switch_agent.
	"send_file":           {}, // ADR-071 D3: promoted from lazy.
	"message_parent":      {}, // ADR-071 D3: promoted from lazy.
	"remember":            {},
	"recall_memory":       {},
	"recall_conversation": {}, // ADR-071 D3: promoted — recall must not cost a discovery round trip.
	"set_todos":           {},
	"list_tasks":          {},
	"delegate":            {}, // ADR-053: must be as visible as create_task/list_tasks/update_task,
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

// ManifestVisibility controls whether a ManifestLazy tool appears as a
// preview line in the compressed manifest block (ADR-071 D3 §4.4). It is a
// SECOND axis, orthogonal to ManifestTier: it is only meaningful for
// ManifestLazy tools, and callers must resolve the tier first. Full and
// Infra tools have no manifest presence at all, so visibility does not apply
// to them.
//
// Do NOT fold this into ManifestTier as a fourth constant:
// SnapshotSearchableTools admits a core tool into the BM25 corpus on
// `ToolManifestTier(name) == ManifestLazy`, so a separate TIER value would
// silently delete every search-only tool from the search index — the one
// mechanism by which it is reachable at all. The corpus is the set of tools
// that exist to be found, not the set a given agent may see or load; both of
// those are decided downstream of ranking (see also §3.2.2's identical
// lesson for policy).
type ManifestVisibility int

const (
	// ManifestPreviewed — Tier 2: one `name — description` line in the block.
	ManifestPreviewed ManifestVisibility = iota
	// ManifestSearchOnly — Tier 3: no line in the block; reachable only via
	// ToolSearch (exact name or query). Still policy-governed and still in
	// the BM25 corpus. Once loaded it stays loaded for the rest of the
	// session: static tools are IsCore, and PromoteTools/TickTTL are no-ops
	// on core entries, so the registry TTL never applies here (ADR-071
	// §1.1.1 / FR-037).
	ManifestSearchOnly
)

// previewedLazyToolNames is the exact 8-name Tier 2 set (ADR-071 D3 §4.1):
// lazy tools that still render a preview line in the compressed manifest
// block. Everything else lazy resolves to ManifestSearchOnly. Membership is
// pinned by TestVisibility_PreviewedSetIsExactlyEight — adding a tool here
// (or removing one) without updating that test's literal list is a build
// failure by design (FR-034).
var previewedLazyToolNames = map[string]struct{}{
	"list_agents":   {},
	"list_jobs":     {},
	"serve_web":     {},
	"navigate":      {},
	"get_workspace": {},
	"bash":          {}, // ADR-071 D3: demoted from Full — see fullManifestToolNames doc.
	"create_task":   {},
	"update_task":   {},
}

// previewAllLazy backs the PreviewAllLazy config revert (ADR-071 §4.3.1b,
// FR-042): a time-boxed kill switch that restores the pre-D3 behavior where
// every findable lazy tool renders a preview line. Set via SetPreviewAllLazy,
// which callers invoke with the live cfg.Tools.Manifest.PreviewAllLazy value
// on every turn (a single atomic store) so the revert is live and does not
// require a restart, mirroring cfg.Tools.Manifest.Compressed's own per-turn
// read elsewhere in this codebase.
var previewAllLazy atomic.Bool

// SetPreviewAllLazy sets the live PreviewAllLazy revert flag consulted by
// ToolManifestVisibility. Safe to call on every turn; default is false (the
// three-tier split is active). This flag is explicitly time-boxed (FR-043):
// it exists to survive the operator's observation window for the two
// ToolSearch detection counters (omnipus_toolsearch_zero_result_total,
// omnipus_toolsearch_no_followup_total, see compositor.go), not forever, and
// must be deleted in the same change that acts on that data.
func SetPreviewAllLazy(v bool) {
	previewAllLazy.Store(v)
}

// ToolManifestVisibility returns the visibility of a lazy-tier tool
// (ADR-071 D3 §4.4). Defined only for ManifestLazy names; callers must check
// the tier first via ToolManifestTier. When the PreviewAllLazy revert is set,
// every lazy tool resolves to ManifestPreviewed — read HERE, inside the
// single chokepoint, so both manifest-block builders inherit the revert with
// no second branch of their own (FR-042).
func ToolManifestVisibility(name string) ManifestVisibility {
	if previewAllLazy.Load() {
		return ManifestPreviewed
	}
	if _, ok := previewedLazyToolNames[name]; ok {
		return ManifestPreviewed
	}
	return ManifestSearchOnly
}

// PreviewedLazyToolNames returns a sorted copy of the Tier 2 (previewed)
// tool name set. Exported for tests and tooling that needs to enumerate it.
func PreviewedLazyToolNames() []string {
	names := make([]string, 0, len(previewedLazyToolNames))
	for n := range previewedLazyToolNames {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
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
		// ADR-071 D3 §4.4: search-only (Tier 3) lazy tools get zero preview
		// text — they stay fully registered, policy-governed and findable by
		// ToolSearch, but invisible until the agent goes looking. The filter
		// lives here (inside the builder) rather than in the caller so it has
		// exactly one owner; see the ADR's "Where the filter lives" note.
		if ToolManifestVisibility(n) != ManifestPreviewed {
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
	// FR-044: state plainly that more tools exist than are listed here (the
	// search-only Tier 3 set is deliberately unlisted, ADR-071 D3 §4.3) and
	// that ToolSearch's `query` finds them by description. Kept to exactly
	// one line — FR-033 requires the header stay at 2 lines total, so this
	// wording is a reword of the existing second line, never a third.
	sb.WriteString(
		"These tools are available but not loaded — call `ToolSearch` with its exact name in `names` to load one, then call it. Many more tools than are listed here exist; describe what you need in `query` to find and load them.\n",
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
