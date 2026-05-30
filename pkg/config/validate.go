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
	"os"
	"path/filepath"
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

	// FR-062: check always runs so an empty DefaultPolicy is caught and audited.
	builtin := onDisk.Tools.Builtin
	check("tools.builtin.default_policy", builtin.DefaultPolicy)
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
