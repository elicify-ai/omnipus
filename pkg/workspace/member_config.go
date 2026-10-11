// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import "fmt"

// MaxHeartbeatBodyBytes is the maximum allowed size of a heartbeat body (16 KB).
// Writes exceeding this cap are rejected with a 422 (FR-005).
const MaxHeartbeatBodyBytes = 16384

// MemberHeartbeat holds the per-(workspace, agent) heartbeat configuration
// (FR-001). When Enabled is true and Body is non-empty, the heartbeat
// reconciler creates a job keyed `heartbeat:<ws>:<agent>` that fires the
// agent in this workspace at the configured cadence using Body as the prompt.
//
// It carries NO session address (session-core U1, DEL-01): a heartbeat runs in
// the member's main session, which is COMPUTED from the (workspace, agent)
// pair (pkg/session/main_session.go::MainSessionID) rather than stored beside
// the configuration. The retired SessionID field was the eager standing
// session's address; with the heartbeat-only session gone, configuration and
// identity no longer need to be kept in sync by hand, and no disable path has
// a session to release.
type MemberHeartbeat struct {
	Enabled         bool   `json:"enabled,omitempty"`
	IntervalMinutes int    `json:"interval_minutes,omitempty"`
	Body            string `json:"body,omitempty"`
}

// MemberConfig is the per-member configuration stored on a Workspace record
// (keyed by agent ID in MemberConfigs). Carries the heartbeat settings and the
// display-only sidebar flag.
//
// Merge semantics (ADR-hide-from-sidebar): an incoming member_config replaces
// the stored one ONLY in the fields it actually carries — a field the body
// omits keeps the stored value (hidden_from_sidebar AND the heartbeat). So the
// one-click hide body ({"hidden_from_sidebar":true}) cannot wipe a stored
// heartbeat, and a heartbeat-only body cannot clear the flag. A heartbeat the
// body DOES carry keeps its documented wholesale-replace semantics (no
// field-by-field heartbeat merge).
type MemberConfig struct {
	Heartbeat *MemberHeartbeat `json:"heartbeat,omitempty"`

	// HiddenFromSidebar is DISPLAY-ONLY (D5): when true the sidebar omits the
	// member's row in this workspace, but the member stays a full team member
	// — messageable, delegable, @-addressable; main and work untouched. It
	// rides the member entry, so removing the member garbage-collects the
	// flag with the entry and re-adding the agent starts visible. omitempty
	// so records that never hid anyone serialize exactly as before.
	HiddenFromSidebar bool `json:"hidden_from_sidebar,omitempty"`
}

// ValidateMemberConfigs checks a proposed member_configs map against the
// workspace's current CoreTeam and the isWorker predicate.
//
// Validation rules (FR-003/004/005/005b/025, plus the sidebar flag from
// ADR-hide-from-sidebar):
//   - Every key must be present in coreTeam (unknown-member reject).
//   - A worker agent (isWorker returns true) must not have a heartbeat.
//   - A worker agent must not be hidden from the sidebar: sidebar rows exist
//     only for main-eligible colleagues, so the flag would be inert there —
//     mirroring the worker-heartbeat rule keeps the API honest.
//   - When a heartbeat is enabled, IntervalMinutes must be ≥ 5.
//   - Body length must not exceed MaxHeartbeatBodyBytes.
//   - Enabled=true requires a non-empty Body.
//
// Returns the first validation error encountered, or nil if all entries are
// valid. Callers (gateway handler) map these to HTTP 422 responses.
func ValidateMemberConfigs(coreTeam []string, mc map[string]MemberConfig, isWorker func(agentID string) bool) error {
	teamSet := make(map[string]bool, len(coreTeam))
	for _, id := range coreTeam {
		teamSet[id] = true
	}

	for agentID, cfg := range mc {
		if !teamSet[agentID] {
			return fmt.Errorf("member_configs: agent %q is not in core_team (unknown member)", agentID)
		}
		if cfg.HiddenFromSidebar && isWorker != nil && isWorker(agentID) {
			return fmt.Errorf("member_configs: agent %q is a worker and cannot be hidden from the sidebar", agentID)
		}
		hb := cfg.Heartbeat
		if hb == nil {
			continue
		}
		if isWorker != nil && isWorker(agentID) {
			return fmt.Errorf("member_configs: agent %q is a worker and cannot have a heartbeat config", agentID)
		}
		// M-1: always enforce the 16KB body cap, even when disabled.
		if len(hb.Body) > MaxHeartbeatBodyBytes {
			return fmt.Errorf(
				"member_configs: agent %q heartbeat body exceeds cap (%d > %d bytes)",
				agentID,
				len(hb.Body),
				MaxHeartbeatBodyBytes,
			)
		}
		// M-1: only enforce interval ≥ 5 and non-empty body when the heartbeat
		// is enabled. A disabled heartbeat with interval=0 and empty body is valid.
		if hb.Enabled {
			if hb.IntervalMinutes < 5 {
				return fmt.Errorf(
					"member_configs: agent %q heartbeat interval_minutes must be ≥ 5 (got %d)",
					agentID,
					hb.IntervalMinutes,
				)
			}
			if hb.Body == "" {
				return fmt.Errorf("member_configs: agent %q heartbeat body is required when enabled", agentID)
			}
		}
	}
	return nil
}

// GCMemberConfigs removes entries from mc whose key is not present in
// coreTeam. It returns the pruned map and a slice of the removed agent IDs.
// The original map is not mutated; a new map is returned. Called by the
// gateway workspace handler when agents are removed from CoreTeam (FR-022).
func GCMemberConfigs(coreTeam []string, mc map[string]MemberConfig) (map[string]MemberConfig, []string) {
	teamSet := make(map[string]bool, len(coreTeam))
	for _, id := range coreTeam {
		teamSet[id] = true
	}

	pruned := make(map[string]MemberConfig, len(mc))
	var removed []string
	for agentID, cfg := range mc {
		if teamSet[agentID] {
			pruned[agentID] = cfg
		} else {
			removed = append(removed, agentID)
		}
	}
	return pruned, removed
}
