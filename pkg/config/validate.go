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
	sb.WriteString(fmt.Sprintf("BOOT_ABORT_REASON=%s", event))
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
		sb.WriteString(fmt.Sprintf(" %s=%v", k, fields[k]))
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
//     gap list logged, so the failure is immediately actionable. Since
//     upgrading installations can have sparse, pre-existing policy maps
//     (from before the DefaultPolicy/default_policy fallback was removed),
//     the boot sequence should call RepairIncompleteToolPolicyCoverage
//     immediately before this function — see that function's doc comment
//     for the exact call site.
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

// RepairIncompleteToolPolicyCoverage backfills any missing tool-policy
// coverage in cfg.Agents.List with explicit "deny" entries (the safe,
// fail-closed direction) before ValidateToolPolicyCoverage is enforced.
// This exists to migrate installations whose on-disk config predates the
// removal of the DefaultPolicy/default_policy fallback (CLAUDE.md hard
// constraint 6) — those agents have sparse Policies maps that relied on
// the deleted default field to cover every unlisted tool. Without this
// repair, ValidateToolPolicyCoverage would find a gap for nearly every
// static tool on every such agent and abort boot, bricking any existing
// installation on upgrade.
//
// Intended call site: pkg/gateway/gateway.go's shared
// repairAndValidateToolPolicyCoverage helper, immediately BEFORE the existing
// config.ValidateToolPolicyCoverage(cfg, buildKnownBuiltinToolNames()) call.
// That helper is called identically from both the boot sequence
// (RunContextWithOptions) and the hot-reload path (executeReload):
//
//	func repairAndValidateToolPolicyCoverage(cfg *config.Config) []config.CoverageGap {
//	    knownTools := buildKnownBuiltinToolNames()
//	    if repaired := config.RepairIncompleteToolPolicyCoverage(cfg, knownTools); len(repaired) > 0 {
//	        // ... log a WARN naming every backfilled (agent, tool) pair ...
//	    }
//	    return config.ValidateToolPolicyCoverage(cfg, knownTools)
//	}
//
// Calling the repair first means the validation call should almost always
// find zero gaps afterward — the repair IS the fix; validation remains as
// the hard backstop for any gap the repair (by construction) cannot close,
// e.g. if knownTools itself is empty/nil.
//
// Mutates cfg.Agents.List in place. For every agent, for every name in
// knownTools not covered by either the global cfg.Sandbox.ToolPolicies map
// or that agent's own Tools.Builtin.Policies map, adds an explicit "deny"
// entry to the AGENT's map (never touches the global map — a missing global
// entry is not this function's concern, since per-agent coverage alone is
// sufficient). Logs one WARN per repaired agent naming the count and list of
// backfilled tools, so operators can review/loosen the auto-tightened policy
// afterward via the UI. Idempotent — a fully-covered config is a no-op, and
// repeated calls after the first repair find nothing left to backfill.
//
// Returns every (AgentID, ToolName) pair that was actually backfilled — the
// exact gap list ValidateToolPolicyCoverage(cfg, knownTools) returned
// immediately before the repair ran (every gap found gets backfilled to
// "deny", so the two lists are identical in content). Empty/nil when cfg or
// knownTools is nil/empty, or when coverage was already complete — callers
// that only care about "was anything repaired" can check len(repaired) > 0,
// while callers that want structured detail (e.g. a future UI review screen)
// get real (agent, tool) pairs instead of a bare count.
func RepairIncompleteToolPolicyCoverage(cfg *Config, knownTools map[string]struct{}) (repaired []CoverageGap) {
	if cfg == nil || len(knownTools) == 0 {
		return nil
	}

	// Delegate the "(agent, tool) is covered?" predicate entirely to
	// ValidateToolPolicyCoverage rather than re-deriving it here — the two
	// functions must never be able to silently diverge on what "covered"
	// means. The returned gaps are already sorted by (AgentID, ToolName).
	gaps := ValidateToolPolicyCoverage(cfg, knownTools)
	if len(gaps) == 0 {
		return nil
	}

	agentIndex := make(map[string]*AgentConfig, len(cfg.Agents.List))
	for i := range cfg.Agents.List {
		agentIndex[cfg.Agents.List[i].ID] = &cfg.Agents.List[i]
	}

	// Backfill every gap to an explicit "deny" entry on the agent's OWN
	// Tools.Builtin.Policies map (never the global map — a missing global
	// entry is not this function's concern; per-agent coverage alone is
	// sufficient, exactly as before). Track per-agent tool names purely for
	// the one-WARN-per-agent log below; agentOrder preserves first-seen
	// order from gaps (already AgentID-sorted) for deterministic log output.
	agentOrder := make([]string, 0)
	backfilledByAgent := make(map[string][]string)
	for _, gap := range gaps {
		agentCfg, ok := agentIndex[gap.AgentID]
		if !ok {
			// Defensive: unreachable in practice since gaps are derived from
			// cfg.Agents.List itself, but never mutate a nonexistent agent.
			continue
		}
		if agentCfg.Tools == nil {
			agentCfg.Tools = &AgentToolsCfg{}
		}
		if agentCfg.Tools.Builtin.Policies == nil {
			agentCfg.Tools.Builtin.Policies = make(map[string]ToolPolicy)
		}
		agentCfg.Tools.Builtin.Policies[gap.ToolName] = ToolPolicyDeny

		if _, seen := backfilledByAgent[gap.AgentID]; !seen {
			agentOrder = append(agentOrder, gap.AgentID)
		}
		backfilledByAgent[gap.AgentID] = append(backfilledByAgent[gap.AgentID], gap.ToolName)
	}

	for _, agentID := range agentOrder {
		toolNames := backfilledByAgent[agentID]
		sort.Strings(toolNames)
		slog.Warn(
			"config: agent had incomplete tool-policy coverage; backfilled missing tools to \"deny\" "+
				"(migration from pre-DefaultPolicy-removal config shape; CLAUDE.md hard constraint 6 — "+
				"review/loosen via the UI if any backfilled tool should actually be allow/ask)",
			"agent_id", agentID,
			"backfilled_count", len(toolNames),
			"backfilled_tools", toolNames,
		)
	}

	return gaps
}
