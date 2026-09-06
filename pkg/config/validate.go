// Omnipus — Boot-time agent config validation
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package config — ValidateAgentConfigs implements Boot Order step 4 from the
// tool-registry-redesign spec (FR-023, FR-049, FR-057, FR-062, FR-063, FR-085).
//
// Called once at boot after the constructor-seed disposition map (Boot Order
// step 3) is computed and before per-agent policy pointers are stored.
//
// Validation rules applied to each agent.json on disk:
//  1. File read failure (permissions/OS lock) → same disposition as parse failure.
//  2. Invalid JSON → HIGH audit "agent.config.corrupt"; skip or abort (see below).
//  3. Invalid policy value (not in {"allow","ask","deny",""}) → HIGH audit
//     "agent.config.invalid_policy_value"; skip or abort.
//  4. Empty-string policy value → HIGH audit "agent.config.invalid_policy_value"
//     (FR-085: empty string is invalid, not silently coerced to "allow").
//  5. Unknown tool names in policies → WARN audit "agent.config.unknown_tool_in_policy"
//     (FR-057); boot continues.
//
// Skip-or-abort disposition (FR-023, FR-062):
//   - Agents whose constructor seed contains explicit system.* allow entries
//     (currently only Ava) → gateway exits non-zero on any validation failure.
//   - All other agents → agent not activated; boot continues.
//
// Audit-emit failure during abort (FR-063): if the audit logger is nil or Log
// returns an error, a structured stderr line is printed before os.Exit:
//
//	BOOT_ABORT_REASON=<event> agent_id=<id> path=<path> error=<msg>

package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ValidPolicy returns true if the value is a recognized ToolPolicy constant or
// the empty string (which is valid at this layer — callers apply own coercion).
// Empty string is NOT accepted here: FR-085 makes it an error.
func ValidPolicy(p ToolPolicy) bool {
	switch p {
	case ToolPolicyAllow, ToolPolicyAsk, ToolPolicyDeny:
		return true
	default:
		return false
	}
}

// AgentConfigOnDisk is the minimal schema of a stored agent.json used by the
// boot validator. Only the fields relevant to policy validation are read.
type AgentConfigOnDisk struct {
	ID    string         `json:"id"`
	Tools *AgentToolsCfg `json:"tools,omitempty"`
}

// AgentValidationResult describes the outcome of validating one agent.json.
type AgentValidationResult struct {
	AgentID      string
	Path         string
	IsCritical   bool     // true if this agent has system.* allows in constructor seed
	FileErr      error    // non-nil if file could not be read
	ParseErr     error    // non-nil if JSON could not be parsed
	PolicyErrors []string // per-field invalid-value messages
	UnknownTools []string // tool names in policies that are not in the registered set
	Valid        bool     // true if file parsed and all values were valid
}

// AuditEmitter is the minimal interface for emitting audit events during boot.
// This avoids an import of pkg/audit in pkg/config (import-cycle prevention).
type AuditEmitter interface {
	// EmitRaw emits a raw audit event. Returns non-nil if emission failed.
	EmitRaw(event, severity string, fields map[string]any) error
}

// ValidateAgentConfigs walks agentsDir, reads each agent.json, and validates
// the policy values. It calls hasSystemAllows(agentID) to determine the
// abort-vs-skip disposition (FR-062).
//
// Parameters:
//   - agentsDir: the root directory containing per-agent subdirectories
//     (each containing an optional agent.json). Typically ~/.omnipus/agents/.
//   - hasSystemAllows: the predicate from coreagent.HasSystemAllowsInConstructorSeed.
//     Returning true means a validation failure on that agent aborts boot.
//   - knownTools: set of tool names registered in the central builtin registry.
//     Used for FR-057 unknown-tool warnings. May be nil (skip unknown-tool check).
//   - auditLog: nil if the audit subsystem is not yet available; the validator
//     falls back to stderr (FR-063).
//
// Returns:
//   - results: one AgentValidationResult per agent.json found.
//   - abortBoot: true if the caller must exit non-zero.
func ValidateAgentConfigs(
	agentsDir string,
	hasSystemAllows func(agentID string) bool,
	knownTools map[string]struct{},
	auditLog AuditEmitter,
) (results []AgentValidationResult, abortBoot bool) {
	entries, err := os.ReadDir(agentsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false // no agents directory — fresh install, nothing to validate
		}
		// Cannot read agents dir at all; this is not the same as a corrupt agent
		// file, but we log and continue.
		emitOrStderr(auditLog, "agent.config.dir_read_error", "HIGH", map[string]any{
			"path":  agentsDir,
			"error": err.Error(),
		})
		return nil, false
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		agentID := entry.Name()
		agentPath := filepath.Join(agentsDir, agentID, "agent.json")

		result := AgentValidationResult{
			AgentID:    agentID,
			Path:       agentPath,
			IsCritical: hasSystemAllows != nil && hasSystemAllows(agentID),
		}

		// Try to read and parse the agent.json. Missing file is not an error:
		// the agent simply uses constructor defaults (Boot Order step 7, dataset row 7/8).
		raw, readErr := os.ReadFile(agentPath) // #nosec G304 — path is under agentsDir
		if readErr != nil {
			if os.IsNotExist(readErr) {
				// No agent.json — constructor seeds apply; not an error.
				result.Valid = true
				results = append(results, result)
				continue
			}
			// Inaccessible file — same disposition as parse failure (FR-023 AS-3).
			result.FileErr = readErr
			result.Valid = false
			event := "agent.config.corrupt"
			agentType := agentTypeLabel(result.IsCritical)
			fields := map[string]any{
				"agent_id":   agentID,
				"agent_type": agentType,
				"path":       agentPath,
				"error":      readErr.Error(),
			}
			emitOrStderr(auditLog, event, "HIGH", fields)
			results = append(results, result)
			if result.IsCritical {
				abortBoot = true
			}
			continue
		}

		// Parse the JSON.
		var onDisk AgentConfigOnDisk
		if parseErr := json.Unmarshal(raw, &onDisk); parseErr != nil {
			result.ParseErr = parseErr
			result.Valid = false
			agentType := agentTypeLabel(result.IsCritical)
			fields := map[string]any{
				"agent_id":   agentID,
				"agent_type": agentType,
				"path":       agentPath,
				"error":      parseErr.Error(),
			}
			emitOrStderr(auditLog, "agent.config.corrupt", "HIGH", fields)
			results = append(results, result)
			if result.IsCritical {
				abortBoot = true
			}
			continue
		}

		// Validate policy values (FR-049, FR-085).
		result.PolicyErrors = validatePolicyValues(agentID, agentPath, onDisk, auditLog)
		if len(result.PolicyErrors) > 0 {
			result.Valid = false
			if result.IsCritical {
				abortBoot = true
			}
			results = append(results, result)
			continue
		}

		// Warn on unknown tool names (FR-057).
		if knownTools != nil && onDisk.Tools != nil {
			for toolName := range onDisk.Tools.Builtin.Policies {
				// Skip wildcard keys (they may reference tools not yet registered).
				if strings.HasSuffix(toolName, ".*") {
					continue
				}
				if _, known := knownTools[toolName]; !known {
					result.UnknownTools = append(result.UnknownTools, toolName)
				}
			}
			if len(result.UnknownTools) > 0 {
				emitOrStderr(auditLog, "agent.config.unknown_tool_in_policy", "WARN", map[string]any{
					"agent_id":      agentID,
					"path":          agentPath,
					"unknown_tools": result.UnknownTools,
				})
			}
		}

		result.Valid = true
		results = append(results, result)
	}

	return results, abortBoot
}

// validatePolicyValues checks all ToolPolicy fields in the agent's on-disk
// config for invalid or empty values. Returns a non-empty slice of error
// descriptions when any field is invalid; emits HIGH audit for each error.
func validatePolicyValues(
	agentID, path string,
	onDisk AgentConfigOnDisk,
	auditLog AuditEmitter,
) []string {
	if onDisk.Tools == nil {
		return nil
	}
	var errs []string

	check := func(fieldPath string, p ToolPolicy) {
		if p == "" {
			// FR-085: empty string is invalid (no longer silently coerced).
			msg := fmt.Sprintf(
				"field %q: empty policy value is not allowed (use \"allow\", \"ask\", or \"deny\")",
				fieldPath,
			)
			errs = append(errs, msg)
			emitOrStderr(auditLog, "agent.config.invalid_policy_value", "HIGH", map[string]any{
				"agent_id": agentID,
				"path":     path,
				"field":    fieldPath,
				"value":    "",
				"error":    msg,
			})
			return
		}
		if !ValidPolicy(p) {
			msg := fmt.Sprintf(
				"field %q: invalid policy value %q (must be \"allow\", \"ask\", or \"deny\")",
				fieldPath,
				p,
			)
			errs = append(errs, msg)
			emitOrStderr(auditLog, "agent.config.invalid_policy_value", "HIGH", map[string]any{
				"agent_id": agentID,
				"path":     path,
				"field":    fieldPath,
				"value":    string(p),
				"error":    msg,
			})
		}
	}

	builtin := onDisk.Tools.Builtin
	for toolName, policy := range builtin.Policies {
		check(fmt.Sprintf("tools.builtin.policies[%q]", toolName), policy)
	}

	return errs
}

// agentTypeLabel returns "core" for critical agents and "custom" for others.
// Used in audit event fields.
func agentTypeLabel(isCritical bool) string {
	if isCritical {
		return "core"
	}
	return "custom"
}

// emitOrStderr emits an audit event via auditLog when available, or prints a
// structured stderr line when auditLog is nil or emission fails (FR-063).
// Format: BOOT_ABORT_REASON=<event> <key>=<value> ...
func emitOrStderr(auditLog AuditEmitter, event, severity string, fields map[string]any) {
	if auditLog != nil {
		if err := auditLog.EmitRaw(event, severity, fields); err == nil {
			return
		}
	}
	// Fall back to stderr — always succeeds for boot-abort visibility.
	var sb strings.Builder
	fmt.Fprintf(&sb, "BOOT_ABORT_REASON=%s", event)
	// Sort keys for deterministic output.
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	// simple sort: range twice is acceptable for the small field sets here
	for i := 0; i < len(keys)-1; i++ {
		for j := i + 1; j < len(keys); j++ {
			if keys[i] > keys[j] {
				keys[i], keys[j] = keys[j], keys[i]
			}
		}
	}
	for _, k := range keys {
		fmt.Fprintf(&sb, " %s=%v", k, fields[k])
	}
	fmt.Fprintln(os.Stderr, sb.String())
}

// NOTE (ADR-054 D6.4): RepairMultipleDefaults used to live here — it enforced
// an at-most-one AgentConfig.Default==true invariant across cfg.Agents.List,
// called from loadConfigInternal (F11). It is retired, not merely renamed:
// once agents split into independent per-entity files, "at most one
// Default==true" is no longer an invariant anything needs enforced, because
// nothing resolves the routing default from that per-entity field anymore —
// AgentRegistry.GetDefaultAgent and RouteResolver.resolveDefaultAgentID both
// consult only the settings singleton (Agents.Defaults.DefaultAgentID) now.
// A per-entity bool could never be a sound at-most-one singleton once two
// different agents' files can be written concurrently with no shared lock
// (each write's delta is individually valid, the composition — two "the
// defaults" — is not); moving the signal to a single settings string removes
// the invariant instead of needing a new cross-entity lock to guard it.

// RepairStaleChannelWildcardBindings enforces the ADR-029 FR-029 two-representation
// rule: a channel instance persists routing EITHER as cfg.Channels[id].{WorkspaceID,
// Identity} (bound) OR as a channel-wildcard AgentBinding in cfg.Bindings (unbound).
// The two are mutually exclusive per instance. When both are present (e.g. an
// in-flight migration or a hand-edited config), the bound representation wins and the
// stale wildcard binding is removed. Idempotent; mutates cfg.Bindings in-place.
// Mirrors the RepairMultipleDefaults pattern (OBS-001).
func RepairStaleChannelWildcardBindings(cfg *Config) {
	if cfg == nil {
		return
	}
	// Build a set of instance IDs that carry the bound representation:
	// WorkspaceID is non-empty AND Identity.Kind == "agent" (non-empty ID).
	boundIDs := make(map[string]struct{})
	for id, inst := range cfg.Channels {
		if inst.WorkspaceID == "" {
			continue
		}
		if inst.Identity == nil {
			continue
		}
		if strings.ToLower(strings.TrimSpace(inst.Identity.Kind)) != ChannelIdentityKindAgent {
			continue
		}
		if strings.TrimSpace(inst.Identity.ID) == "" {
			continue
		}
		boundIDs[strings.ToLower(id)] = struct{}{}
	}
	if len(boundIDs) == 0 {
		return // nothing to repair
	}

	// Scan cfg.Bindings and drop wildcard entries whose channel matches a bound id.
	var kept []AgentBinding
	var dropped []string
	for _, b := range cfg.Bindings {
		ch := strings.ToLower(strings.TrimSpace(b.Match.Channel))
		acc := strings.TrimSpace(b.Match.AccountID)
		isWildcard := acc == "*" && b.Match.Peer == nil && b.Match.GuildID == "" && b.Match.TeamID == ""
		if isWildcard {
			if _, bound := boundIDs[ch]; bound {
				dropped = append(dropped, ch)
				continue
			}
		}
		kept = append(kept, b)
	}
	if len(dropped) == 0 {
		return
	}
	sort.Strings(dropped)
	slog.Warn(
		"config: channel instances have both bound Identity and wildcard binding; removing stale wildcard binding(s) (ADR-029 FR-029)",
		"instance_ids",
		dropped,
	)
	cfg.Bindings = kept
}

// CoverageGap identifies a single (agent, tool) pair found to have no
// explicit tool-policy entry by ValidateToolPolicyCoverage — the structured
// form of a coverage-validation finding. Exposing AgentID and ToolName as
// fields (rather than only a formatted string) lets callers filter, group,
// or count gaps programmatically — e.g. counting gaps per agent, or checking
// whether a specific tool is the culprit — without parsing String() output.
type CoverageGap struct {
	AgentID  string
	ToolName string
}

// String renders the gap as a human-readable message, matching the format
// previously returned as a raw string by ValidateToolPolicyCoverage before
// it was changed to return []CoverageGap. Implementing fmt.Stringer means
// a single gap formats correctly via %s/%v, but a []CoverageGap slice does
// NOT automatically join into the old comma-joined-string shape — callers
// that need a []string (e.g. for strings.Join or a log field) must map
// explicitly:
//
//	msgs := make([]string, len(gaps))
//	for i, g := range gaps {
//	    msgs[i] = g.String()
//	}
//
// This explicit mapping step is intentional: it is the smallest possible
// adaptation for existing %s/strings.Join call sites while giving new code
// direct field access instead of string parsing.
func (g CoverageGap) String() string {
	return fmt.Sprintf("agent %q: no policy entry for tool %q (global or per-agent)", g.AgentID, g.ToolName)
}

// ValidateToolPolicyCoverage enforces CLAUDE.md hard constraint 6: every
// static builtin tool in knownTools must resolve, for every agent in
// cfg.Agents.List, from an explicit, literal (exact-match, wildcard-free)
// policy entry — either globally (cfg.Sandbox.ToolPolicies[name]) or on the
// agent itself (agent.Tools.Builtin.Policies[name]). Only one of the two
// needs the entry; having it on either side counts as covered — the two
// layers are NOT required to agree on the same value, only for at least one
// of them to have an entry at all (see
// TestValidateToolPolicyCoverage_BothSidesPresentDifferentValues_NotAGap).
// Wildcard keys (trailing ".*"/"_*") do NOT count as coverage here —
// wildcards remain valid only for MCP per-server bulk policies, which are
// not part of the static builtin catalog knownTools represents.
//
// This is a pure, side-effect-free check: it returns every gap found rather
// than stopping at the first one, so callers can report a complete
// "agent × tool" list in a single validation pass. Callers decide the
// disposition:
//   - Boot (pkg/gateway/gateway.go): any gap aborts startup with the full
//     gap list logged, so the failure is immediately actionable. Under the
//     ADR-077 two-layer model this is a never-firing correctness tripwire:
//     the boot sequence calls ReconcileToolPolicyCeiling immediately before
//     this function, which keeps the GLOBAL ceiling complete for the whole
//     static catalog, so a gap here can only mean a genuine internal drift
//     (a catalog tool with no defaults.go entry) rather than a normal
//     upgrade state.
//   - Agent create/update/tools-write REST handlers: any gap rejects the
//     write with 400 instead of silently persisting an uncovered tool.
//
// Returns nil (or an empty slice) when coverage is complete. A nil or empty
// knownTools is treated as "nothing to check" (returns nil) rather than as
// a universal gap — the caller is responsible for supplying the real catalog.
func ValidateToolPolicyCoverage(cfg *Config, knownTools map[string]struct{}) []CoverageGap {
	if cfg == nil || len(knownTools) == 0 {
		return nil
	}
	var gaps []CoverageGap
	for _, agentCfg := range cfg.Agents.List {
		var agentPolicies map[string]ToolPolicy
		if agentCfg.Tools != nil {
			agentPolicies = agentCfg.Tools.Builtin.Policies
		}
		for toolName := range knownTools {
			if _, ok := cfg.Sandbox.ToolPolicies[toolName]; ok {
				continue // global exact-match entry covers it
			}
			if _, ok := agentPolicies[toolName]; ok {
				continue // per-agent exact-match entry covers it
			}
			gaps = append(gaps, CoverageGap{AgentID: agentCfg.ID, ToolName: toolName})
		}
	}
	sort.Slice(gaps, func(i, j int) bool {
		if gaps[i].AgentID != gaps[j].AgentID {
			return gaps[i].AgentID < gaps[j].AgentID
		}
		return gaps[i].ToolName < gaps[j].ToolName
	})
	return gaps
}

// MCPToolPolicyKeyPrefix is the namespace every MCP-server tool name and every
// MCP per-server bulk (wildcard) policy key carries — pkg/tools/mcp_tool.go
// composes tool names as "mcp_<server>_<tool>", and the SPA's per-server bulk
// control derives its wildcard key from the longest common underscore prefix of
// those names (src/components/shared/ToolPolicyEditor.tsx), i.e.
// "mcp_<server>_*".
//
// This prefix is the ONE carve-out to the "keys must be known static builtin
// tool names" rule in ValidateSubmittedToolPolicyMap. CLAUDE.md hard constraint
// 6 states it explicitly: MCP-server tool names are not knowable until an
// operator connects the server at runtime, so they cannot be statically
// pre-enumerated and the per-server wildcard bulk policy remains valid there.
// The no-wildcard rule applies to the static builtin catalog only.
const MCPToolPolicyKeyPrefix = "mcp_"

// SubmittedToolPolicyDefects is the verdict ValidateSubmittedToolPolicyMap
// returns for one caller-supplied per-tool policy map. Both fields are sorted
// so the resulting 400 body is deterministic (and therefore assertable in a
// test) rather than dependent on Go map iteration order.
type SubmittedToolPolicyDefects struct {
	// Missing lists every static builtin tool name that the submitted map does
	// NOT carry an explicit, literal key for.
	Missing []string
	// Invalid lists every submitted key that is neither a known static builtin
	// tool name nor an MCP-namespaced key — wildcards ("*", "system.*",
	// "browser_*") and typos both land here.
	Invalid []string
}

// Empty reports whether the submitted map was fully complete and free of
// unrecognized/wildcard keys — i.e. whether the write may proceed.
func (d SubmittedToolPolicyDefects) Empty() bool {
	return len(d.Missing) == 0 && len(d.Invalid) == 0
}

// String renders the defects as a single human-readable, operator-actionable
// sentence naming the offending tool names, for use as an HTTP 400 body.
// Long lists are truncated at maxNamedToolsInDefectMessage names plus a
// "+N more" tail so a 88-tool gap does not produce an unreadable error, while
// the exact counts stay exact.
func (d SubmittedToolPolicyDefects) String() string {
	var parts []string
	if len(d.Missing) > 0 {
		parts = append(parts, fmt.Sprintf(
			"is incomplete — %d static builtin tool(s) have no explicit policy entry: %s",
			len(d.Missing), truncateToolNameList(d.Missing),
		))
	}
	if len(d.Invalid) > 0 {
		parts = append(parts, fmt.Sprintf(
			"contains %d key(s) that are neither a known static builtin tool name nor an %s-namespaced MCP key "+
				"(wildcards are not valid for the static builtin catalog): %s",
			len(d.Invalid), MCPToolPolicyKeyPrefix, truncateToolNameList(d.Invalid),
		))
	}
	if len(parts) == 0 {
		return "is complete"
	}
	return strings.Join(parts, "; and ")
}

// maxNamedToolsInDefectMessage caps how many tool names String() spells out
// before summarizing the remainder as "+N more".
const maxNamedToolsInDefectMessage = 12

func truncateToolNameList(names []string) string {
	if len(names) <= maxNamedToolsInDefectMessage {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s, +%d more",
		strings.Join(names[:maxNamedToolsInDefectMessage], ", "),
		len(names)-maxNamedToolsInDefectMessage,
	)
}

// ValidateSubmittedToolPolicyMap checks ONE caller-supplied per-tool policy map
// — exactly as the caller sent it, before any server-side seed/backfill/merge —
// against the static builtin catalog in knownTools. It answers a deliberately
// narrower question than ValidateToolPolicyCoverage:
//
//	ValidateToolPolicyCoverage: "can every tool resolve from SOME explicit
//	entry, global OR per-agent, for every agent in the roster?"
//
//	ValidateSubmittedToolPolicyMap: "is THIS map, on its own, a complete and
//	well-formed per-agent policy map?"
//
// The distinction is the whole point, and it is why the coverage check alone
// was not enough. The seeded global ceiling (cfg.Sandbox.ToolPolicies,
// pkg/config/defaults.go) carries an explicit entry for every static builtin
// tool on a default install, so ValidateToolPolicyCoverage finds ZERO gaps for
// ANY agent no matter what that agent's own map contains — including an empty
// one. Runtime resolution, however, is a strictest-wins merge in which "one
// side is enough" (pkg/tools/compositor.go's resolveEffectivePolicyWith): a
// tool absent from the agent's own map resolves to the GLOBAL value alone. So a
// write that silently leaves a hole in an agent's map does not fail closed —
// it drops that agent's tightening and inherits the (typically permissive)
// ceiling. Observed live: an agent explicitly denied `bash` had its map wiped
// by a malformed tools write, passed the coverage check because the global map
// covered everything, and then executed `bash` successfully.
//
// Rules, applied to the submitted map only:
//   - Every name in knownTools MUST appear as an explicit, literal key →
//     otherwise reported in Missing.
//   - Every submitted key MUST be either a name in knownTools or an
//     MCPToolPolicyKeyPrefix-namespaced key → otherwise reported in Invalid.
//     This is what rejects a literal "*" (and any other wildcard) for the
//     static builtin catalog, which the merge loops previously copied through
//     verbatim as an inert key.
//
// Policy VALUES are not checked here — the wire schemas constrain them to
// allow/ask/deny and each handler re-checks them; this function is purely about
// key completeness and key legitimacy.
//
// A nil or empty knownTools returns no defects (nothing to check) — the caller
// is responsible for supplying the real catalog, exactly as
// ValidateToolPolicyCoverage does. A nil submitted map with a non-empty
// knownTools is maximally defective (every known tool Missing), which is the
// intended verdict: "the caller sent no policy map at all" must be a rejection,
// never a silent replace-with-empty.
//
// V is constrained to ~string so the same function serves the plain
// map[string]string the REST handlers normalize wire enums into, the generated
// per-request enum types directly, and map[string]ToolPolicy alike.
func ValidateSubmittedToolPolicyMap[V ~string](
	submitted map[string]V,
	knownTools map[string]struct{},
) SubmittedToolPolicyDefects {
	var defects SubmittedToolPolicyDefects
	if len(knownTools) == 0 {
		return defects
	}
	for toolName := range knownTools {
		if _, ok := submitted[toolName]; !ok {
			defects.Missing = append(defects.Missing, toolName)
		}
	}
	for key := range submitted {
		if _, ok := knownTools[key]; ok {
			continue
		}
		if strings.HasPrefix(key, MCPToolPolicyKeyPrefix) {
			continue // MCP carve-out (CLAUDE.md hard constraint 6, MCP exception)
		}
		defects.Invalid = append(defects.Invalid, key)
	}
	sort.Strings(defects.Missing)
	sort.Strings(defects.Invalid)
	return defects
}

// NOTE (ADR-077 D5): ValidateAgentOwnToolPolicyCoverage used to live here — it
// reported every (agent, tool) pair where an agent with its own builtin
// policy map was missing an explicit entry for a static builtin tool (i.e.
// the tool resolved for that agent from the GLOBAL ceiling alone), logged at
// boot as an ERROR by pkg/gateway/gateway.go's
// repairAndValidateToolPolicyCoverage. It is retired, not merely renamed:
// under the ratified two-layer model (ADR-077 D1), an agent riding the global
// ceiling for a tool it never mentions is the NORMAL, intended state — Layer
// 2 (per-agent overrides) is deliberately sparse and only ever tightens
// below the ceiling. An ERROR-level log naming every such pair fired for the
// overwhelming majority of the agent×tool matrix on a healthy install,
// trained operators to ignore it, and asserted a per-agent-completeness
// expectation the model explicitly rejects. The concern it named — a lost
// per-agent tightening — is still guarded at the write boundary by
// ValidateSubmittedToolPolicyMap (agent create/update/tools-write, 400 on an
// incomplete submitted map), which is where a tightening can actually be
// dropped. Removed by operator decision, ADR-077 — do not reintroduce.

// NOTE (ADR-077 D3): RepairIncompleteToolPolicyCoverage used to live here —
// it backfilled any missing (agent, tool) coverage with explicit per-agent
// "deny" entries before ValidateToolPolicyCoverage was enforced, called from
// pkg/gateway/gateway.go's repairAndValidateToolPolicyCoverage immediately
// before that validation. It is retired, not merely renamed: with
// ReconcileToolPolicyCeiling (ADR-076) keeping the GLOBAL ceiling complete
// for the whole static catalog on every load, a both-sides gap for a catalog
// tool became impossible — this function's premise was gone, and it could
// only misfire, silently denying a newly-shipped "allow" tool (e.g. a fresh
// bash=allow default) on some code path Reconcile didn't precede. That is a
// code-branch default in all but name: the answer "deny" was chosen by code,
// not by seeded data, in direct tension with CLAUDE.md hard constraint 6.
// Tool policy is now exactly two layers — the reconciled global ceiling IS
// the default; sparse per-agent overrides only tighten — with no fail-closed
// backfill between them. Removed by operator decision, ADR-077 — do not
// reintroduce.

// legacyToolPolicyKeyMigrations maps a retired tool-policy key to the key it
// folds into. Both ADR-071 renames are handled by the same pass, at the same
// insertion point, on the same boot: D1 (load_tool -> ToolSearch) and D4
// (hand_off / return_to_default -> switch_agent, ADR-071 §5.3 item 5).
var legacyToolPolicyKeyMigrations = map[string]string{
	"load_tool":         "ToolSearch",
	"hand_off":          "switch_agent",
	"return_to_default": "switch_agent",
}

// toolPolicyStrictness ranks a policy value from loosest to strictest so a
// migration fold can pick the strictest of several disagreeing values
// (deny > ask > allow). An unrecognized value ranks below every recognized
// one, so it never wins a fold against a real policy value.
func toolPolicyStrictness(v string) int {
	switch v {
	case string(ToolPolicyDeny):
		return 2
	case string(ToolPolicyAsk):
		return 1
	case string(ToolPolicyAllow):
		return 0
	default:
		return -1
	}
}

// stricterToolPolicyValue returns whichever of a, b is stricter
// (deny > ask > allow). Ties (including two unrecognized values) keep a, so
// the fold is deterministic regardless of iteration order.
func stricterToolPolicyValue(a, b string) string {
	if toolPolicyStrictness(b) > toolPolicyStrictness(a) {
		return b
	}
	return a
}

// migrateLegacyToolPolicyMap rewrites, in place, every legacy key in m that
// legacyToolPolicyKeyMigrations names, folding it into its destination key
// with the strictest-wins rule (stricterToolPolicyValue) and deleting the
// legacy key. The destination key's own pre-existing value (if any)
// participates in the fold too — ADR-071 §5.3.5b's non-obvious rule that
// keeps the fold monotone-strict and therefore safe to re-run against its
// own output (an explicit switch_agent: deny must never be weakened back to
// allow by a stale hand_off: allow key). T is constrained to ~string so the
// same function serves both the global map[string]string ceiling
// (cfg.Sandbox.ToolPolicies) and the per-agent map[string]ToolPolicy
// (AgentBuiltinToolsCfg.Policies) without duplicating the fold logic.
//
// Returns true if m was modified. A nil or legacy-key-free map is a no-op —
// this is what makes the migration idempotent by construction (ADR-071
// §5.3.5b): re-running it against an already-migrated config finds no legacy
// keys and changes nothing.
func migrateLegacyToolPolicyMap[T ~string](m map[string]T) bool {
	if len(m) == 0 {
		return false
	}
	// Group the legacy keys actually present by destination, so multiple
	// legacy keys for the same destination (hand_off + return_to_default ->
	// switch_agent) fold together in one pass.
	byDest := make(map[string][]string)
	for legacy, dest := range legacyToolPolicyKeyMigrations {
		if _, ok := m[legacy]; ok {
			byDest[dest] = append(byDest[dest], legacy)
		}
	}
	if len(byDest) == 0 {
		return false
	}
	for dest, legacyKeys := range byDest {
		merged := string(m[dest]) // "" (unrecognized, never wins) if dest absent.
		for _, legacy := range legacyKeys {
			merged = stricterToolPolicyValue(merged, string(m[legacy]))
			delete(m, legacy)
		}
		m[dest] = T(merged)
	}
	return true
}

// MigrateLegacyToolPolicyKeys rewrites every persisted tool-policy key naming
// a tool retired by ADR-071 — load_tool (D1), hand_off, or return_to_default
// (D4) — to the tool's replacement name, across the global ceiling
// (cfg.Sandbox.ToolPolicies) and every agent's own override map
// (AgentConfig.Tools.Builtin.Policies), taking the strictest value where
// legacy keys (or the destination key itself) disagree, and deleting the
// legacy keys. See migrateLegacyToolPolicyMap's doc comment for the fold
// rule and idempotency argument.
//
// MUST run before ReconcileToolPolicyCeiling, not merely before
// ValidateToolPolicyCoverage (ADR-071 §5.3.5a). ToolSearch and switch_agent
// are new names with no policy entry anywhere until this migration folds the
// legacy keys forward; sequenced any later, ReconcileToolPolicyCeiling would
// reconcile a "missing" entry for the new name under a value derived while
// the legacy key was still present, rather than recognizing it as already
// migrated. The intended call site is gateway.go's shared
// repairAndValidateToolPolicyCoverage helper, as the FIRST statement inside
// it, before config.ReconcileToolPolicyCeiling — that helper already runs
// identically at boot (RunContextWithOptions) and hot-reload (executeReload),
// so a migration placed there cannot diverge between the two call sites.
//
// Mutates cfg in place. Returns true if anything changed — informational
// only; callers are not required to act on it (ReconcileToolPolicyCeiling
// and ValidateToolPolicyCoverage below both re-derive their own view of
// coverage from the mutated cfg regardless).
func MigrateLegacyToolPolicyKeys(cfg *Config) bool {
	if cfg == nil {
		return false
	}
	changed := migrateLegacyToolPolicyMap(cfg.Sandbox.ToolPolicies)
	for i := range cfg.Agents.List {
		agentCfg := &cfg.Agents.List[i]
		if agentCfg.Tools == nil {
			continue
		}
		if migrateLegacyToolPolicyMap(agentCfg.Tools.Builtin.Policies) {
			changed = true
		}
	}
	return changed
}

// ReconcileToolPolicyCeiling backfills cfg.Sandbox.ToolPolicies (the GLOBAL
// tool-policy ceiling) with a real, shipped-default entry for every static
// builtin tool named in knownTools that the ceiling has no entry for yet —
// ADR-076. This closes the gap CLAUDE.md hard constraint 6's "no
// default-policy fallback" model left open on upgrade: an existing install's
// config.json is a point-in-time snapshot of whatever pkg/config/defaults.go
// enumerated at the time it was written, so every static builtin tool added
// to defaults.go afterward is missing from that install's ceiling until
// something adds it. Under the ADR-077 two-layer model this function's
// output IS the default for that tool — there is no further per-agent
// backfill layer to fall back on if it resolves to the wrong thing, so
// reconciling to the real shipped default here matters more, not less: a
// brand-new tool like AskUserQuestion — shipped "allow" — must resolve
// "allow" from this reconciled ceiling on every pre-existing install,
// without an operator needing to notice and edit config.json by hand.
//
// knownTools MUST be the exact same static-catalog set the Constraint-6
// validator uses (buildKnownBuiltinToolNames() at the gateway.go call site)
// — passing the identical map instance, not a re-derived one, is what makes
// "coverage and reconciliation can never disagree" true by construction
// rather than by convention. The shipped default VALUE for each name comes
// from DefaultConfig().Sandbox.ToolPolicies, which pkg/config/defaults.go's
// own doc comment states mirrors pkg/coreagent/core.go's allStaticToolNames
// literal-for-literal — i.e. the same static catalog knownTools represents.
//
// Rules (never deviate from these — see ADR-076 for the rationale):
//   - A tool name in knownTools with NO existing cfg.Sandbox.ToolPolicies
//     entry gets one added, at exactly DefaultConfig()'s shipped value for
//     that name (never a blanket "allow" — e.g. browser_upload_file ships
//     "ask").
//   - A tool name that ALREADY has an entry — operator-set, migrated, or
//     from an earlier reconciliation — is never touched. This function only
//     ever ADDS a missing entry; it never overwrites one, regardless of
//     value.
//   - A key in cfg.Sandbox.ToolPolicies that is NOT in knownTools (a
//     retired/legacy key, an MCP-namespaced key, an operator's own custom
//     entry) is left completely alone — reconciliation is additive-only
//     against the current static catalog, never a cleanup pass.
//   - A name in knownTools that DefaultConfig().Sandbox.ToolPolicies has no
//     entry for (a catalog/defaults drift that TestBuildKnownBuiltinToolNames_
//     MatchesCoreagentStaticToolCatalog guards against separately) is
//     skipped rather than guessed at — this function never invents a value.
//
// Mutates cfg in place; allocates cfg.Sandbox.ToolPolicies if it was nil.
// Returns every "tool=policy" pair actually added, sorted, for the caller to
// log — nil when nothing needed adding, including on a second call over an
// already-reconciled config (idempotent by construction: an added entry is
// present on the next call, so the "no existing entry" guard skips it).
//
// Intended call site: pkg/gateway/gateway.go's shared
// repairAndValidateToolPolicyCoverage helper, immediately AFTER
// MigrateLegacyToolPolicyKeys (so a renamed legacy key is not treated as
// "missing" and reconciled to a possibly-different default) and BEFORE
// ValidateToolPolicyCoverage — the two-layer model (ADR-077) has no further
// backfill step after this one; the reconciled ceiling IS the default.
func ReconcileToolPolicyCeiling(cfg *Config, knownTools map[string]struct{}) []string {
	if cfg == nil || len(knownTools) == 0 {
		return nil
	}
	defaultPolicies := DefaultConfig().Sandbox.ToolPolicies
	if len(defaultPolicies) == 0 {
		return nil
	}
	if cfg.Sandbox.ToolPolicies == nil {
		cfg.Sandbox.ToolPolicies = make(map[string]string, len(knownTools))
	}
	var added []string
	for name := range knownTools {
		if _, ok := cfg.Sandbox.ToolPolicies[name]; ok {
			continue // never overwrite an existing entry, operator-set or otherwise
		}
		defVal, ok := defaultPolicies[name]
		if !ok {
			continue // no shipped default to reconcile from — skip rather than guess
		}
		cfg.Sandbox.ToolPolicies[name] = defVal
		added = append(added, name+"="+defVal)
	}
	sort.Strings(added)
	return added
}
