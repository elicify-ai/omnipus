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
// SessionID is set eagerly by the workspace handler when the heartbeat is
// enabled (FR-010): a standing session is created and its id stored here so
// the cron job can continue it (via JobSpec.SessionID) rather than minting a
// fresh session each run.
type MemberHeartbeat struct {
	Enabled         bool   `json:"enabled,omitempty"`
	IntervalMinutes int    `json:"interval_minutes,omitempty"`
	Body            string `json:"body,omitempty"`
	SessionID       string `json:"session_id,omitempty"` // eager standing session id
}

// MemberConfig is the per-member configuration stored on a Workspace record
// (keyed by agent ID in MemberConfigs). Currently carries only heartbeat
// settings; the struct is extensible for future per-member concerns.
type MemberConfig struct {
	Heartbeat *MemberHeartbeat `json:"heartbeat,omitempty"`
}

// ValidateMemberConfigs checks a proposed member_configs map against the
// workspace's current CoreTeam and the isWorker predicate.
//
// Validation rules (FR-003/004/005/005b/025):
//   - Every key must be present in coreTeam (unknown-member reject).
//   - When a heartbeat is present, IntervalMinutes must be ≥ 5.
//   - Body length must not exceed MaxHeartbeatBodyBytes.
//   - Enabled=true requires a non-empty Body.
//   - A worker agent (isWorker returns true) must not have a heartbeat.
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
		hb := cfg.Heartbeat
		if hb == nil {
			continue
		}
		if isWorker != nil && isWorker(agentID) {
			return fmt.Errorf("member_configs: agent %q is a worker and cannot have a heartbeat config", agentID)
		}
		// M-1: always enforce the 16KB body cap, even when disabled.
		if len(hb.Body) > MaxHeartbeatBodyBytes {
			return fmt.Errorf("member_configs: agent %q heartbeat body exceeds cap (%d > %d bytes)", agentID, len(hb.Body), MaxHeartbeatBodyBytes)
		}
		// M-1: only enforce interval ≥ 5 and non-empty body when the heartbeat
		// is enabled. A disabled heartbeat with interval=0 and empty body is valid.
		if hb.Enabled {
			if hb.IntervalMinutes < 5 {
				return fmt.Errorf("member_configs: agent %q heartbeat interval_minutes must be ≥ 5 (got %d)", agentID, hb.IntervalMinutes)
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
