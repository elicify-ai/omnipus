// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package workspace

import (
	"encoding/json"
	"strings"
	"testing"
)

// workerSet is a helper that returns an isWorker func marking the given ids.
func workerSet(ids ...string) func(string) bool {
	m := make(map[string]bool, len(ids))
	for _, id := range ids {
		m[id] = true
	}
	return func(agentID string) bool { return m[agentID] }
}

// TestValidateMemberConfigs mirrors spec DS-1 (rows 1–9) and the worker/GC
// edge cases from FR-003/004/005/005b/025.
func TestValidateMemberConfigs(t *testing.T) {
	coreTeam := []string{"mia", "jim"}
	noWorker := func(string) bool { return false }

	tests := []struct {
		name      string
		mc        map[string]MemberConfig
		isWorker  func(string) bool
		wantErr   bool
		errSubstr string
	}{
		{
			// DS-1 row 1: valid config — 200
			name: "valid_config_interval_30",
			mc: map[string]MemberConfig{
				"mia": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 30, Body: "ok"}},
			},
			isWorker: noWorker,
			wantErr:  false,
		},
		{
			// DS-1 row 2: interval exactly 5 — accepted (min boundary)
			name: "interval_exactly_5_ok",
			mc: map[string]MemberConfig{
				"mia": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 5, Body: "ok"}},
			},
			isWorker: noWorker,
			wantErr:  false,
		},
		{
			// DS-1 row 3: interval 4 — reject (FR-004)
			name: "interval_4_reject",
			mc: map[string]MemberConfig{
				"mia": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 4, Body: "ok"}},
			},
			isWorker:  noWorker,
			wantErr:   true,
			errSubstr: "interval_minutes must be ≥ 5",
		},
		{
			// DS-1 row 4: interval 0 — reject (FR-004)
			name: "interval_0_reject",
			mc: map[string]MemberConfig{
				"mia": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 0, Body: "ok"}},
			},
			isWorker:  noWorker,
			wantErr:   true,
			errSubstr: "interval_minutes must be ≥ 5",
		},
		{
			// DS-1 row 5: unknown key (not in CoreTeam) — reject (FR-003)
			name: "unknown_key_reject",
			mc: map[string]MemberConfig{
				"ava": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 30, Body: "ok"}},
			},
			isWorker:  noWorker,
			wantErr:   true,
			errSubstr: "not in core_team",
		},
		{
			// DS-1 row 6: body over cap — reject (FR-005)
			name: "body_over_cap_reject",
			mc: map[string]MemberConfig{
				"mia": {Heartbeat: &MemberHeartbeat{
					Enabled: true, IntervalMinutes: 30,
					Body: strings.Repeat("x", MaxHeartbeatBodyBytes+1),
				}},
			},
			isWorker:  noWorker,
			wantErr:   true,
			errSubstr: "body exceeds cap",
		},
		{
			// DS-1 row 7 / E3: enabled=true with empty body — reject (FR-005b)
			name: "enabled_empty_body_reject",
			mc: map[string]MemberConfig{
				"mia": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 30, Body: ""}},
			},
			isWorker:  noWorker,
			wantErr:   true,
			errSubstr: "body is required when enabled",
		},
		{
			// DS-1 row 8: large interval accepted
			name: "large_interval_accepted",
			mc: map[string]MemberConfig{
				"mia": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 100000, Body: "ok"}},
			},
			isWorker: noWorker,
			wantErr:  false,
		},
		{
			// DS-1 row 9: unicode/emoji body accepted
			name: "unicode_body_accepted",
			mc: map[string]MemberConfig{
				"mia": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 30, Body: "hello 🐙"}},
			},
			isWorker: noWorker,
			wantErr:  false,
		},
		{
			// Body at exact cap is accepted (boundary)
			name: "body_exactly_at_cap_ok",
			mc: map[string]MemberConfig{
				"mia": {Heartbeat: &MemberHeartbeat{
					Enabled: true, IntervalMinutes: 30,
					Body: strings.Repeat("x", MaxHeartbeatBodyBytes),
				}},
			},
			isWorker: noWorker,
			wantErr:  false,
		},
		{
			// FR-025: worker agent with a heartbeat — reject
			name: "worker_with_heartbeat_reject",
			mc: map[string]MemberConfig{
				"jim": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 30, Body: "ok"}},
			},
			isWorker:  workerSet("jim"),
			wantErr:   true,
			errSubstr: "worker and cannot have a heartbeat",
		},
		{
			// nil heartbeat entry is fine (disabled by omission)
			name:     "nil_heartbeat_ok",
			mc:       map[string]MemberConfig{"mia": {Heartbeat: nil}},
			isWorker: noWorker,
			wantErr:  false,
		},
		{
			// empty map is valid (no heartbeats configured)
			name:     "empty_map_ok",
			mc:       map[string]MemberConfig{},
			isWorker: noWorker,
			wantErr:  false,
		},
		{
			// nil map is valid
			name:     "nil_map_ok",
			mc:       nil,
			isWorker: noWorker,
			wantErr:  false,
		},
		{
			// disabled heartbeat with empty body is accepted (only enabled requires body)
			name: "disabled_empty_body_ok",
			mc: map[string]MemberConfig{
				"mia": {Heartbeat: &MemberHeartbeat{Enabled: false, IntervalMinutes: 30, Body: ""}},
			},
			isWorker: noWorker,
			wantErr:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateMemberConfigs(coreTeam, tc.mc, tc.isWorker)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.errSubstr)
				}
				if tc.errSubstr != "" && !strings.Contains(err.Error(), tc.errSubstr) {
					t.Fatalf("expected error containing %q, got: %v", tc.errSubstr, err)
				}
			} else {
				if err != nil {
					t.Fatalf("expected no error, got: %v", err)
				}
			}
		})
	}
}

// TestGCMemberConfigs verifies that entries whose key is not in coreTeam are
// removed and that in-team entries are preserved unchanged.
func TestGCMemberConfigs(t *testing.T) {
	mc := map[string]MemberConfig{
		"mia": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 30, Body: "mia body"}},
		"jim": {Heartbeat: &MemberHeartbeat{Enabled: false, IntervalMinutes: 10, Body: "jim body"}},
		"ava": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 60, Body: "ava body"}}, // not in team
	}
	coreTeam := []string{"mia", "jim"}

	pruned, removed := GCMemberConfigs(coreTeam, mc)

	// "ava" must be removed
	if len(removed) != 1 || removed[0] != "ava" {
		t.Fatalf("expected removed=[ava], got %v", removed)
	}
	if _, ok := pruned["ava"]; ok {
		t.Fatal("expected ava to be pruned from result map")
	}
	// "mia" and "jim" must be preserved
	if _, ok := pruned["mia"]; !ok {
		t.Fatal("expected mia to be preserved")
	}
	if _, ok := pruned["jim"]; !ok {
		t.Fatal("expected jim to be preserved")
	}
	// original map must not be mutated
	if _, ok := mc["ava"]; !ok {
		t.Fatal("original map must not be mutated")
	}
}

// TestGCMemberConfigs_AllGone removes every member from the team and expects
// an empty pruned map and all ids in removed.
func TestGCMemberConfigs_AllGone(t *testing.T) {
	mc := map[string]MemberConfig{
		"mia": {Heartbeat: &MemberHeartbeat{Enabled: true, IntervalMinutes: 30, Body: "x"}},
	}
	pruned, removed := GCMemberConfigs([]string{}, mc)
	if len(pruned) != 0 {
		t.Fatalf("expected empty pruned map, got %v", pruned)
	}
	if len(removed) != 1 || removed[0] != "mia" {
		t.Fatalf("expected removed=[mia], got %v", removed)
	}
}

// TestGCMemberConfigs_NilMap verifies GC on a nil map returns empty results
// without panicking.
func TestGCMemberConfigs_NilMap(t *testing.T) {
	pruned, removed := GCMemberConfigs([]string{"mia"}, nil)
	if len(pruned) != 0 {
		t.Fatalf("expected empty pruned map, got %v", pruned)
	}
	if len(removed) != 0 {
		t.Fatalf("expected no removed entries, got %v", removed)
	}
}

// TestWorkspace_MemberConfigs_JSON verifies that MemberConfigs round-trips
// through JSON (FR-001 store & read).
func TestWorkspace_MemberConfigs_JSON(t *testing.T) {
	ws := Workspace{
		ID:       "ws1",
		Name:     "Alpha",
		Status:   "active",
		CoreTeam: []string{"mia"},
		MemberConfigs: map[string]MemberConfig{
			"mia": {
				Heartbeat: &MemberHeartbeat{
					Enabled:         true,
					IntervalMinutes: 30,
					Body:            "hello world",
					SessionID:       "sess-123",
				},
			},
		},
		CreatedAt: "2026-01-01T00:00:00Z",
		UpdatedAt: "2026-01-01T00:00:00Z",
	}

	data, err := json.Marshal(ws)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var ws2 Workspace
	if err := json.Unmarshal(data, &ws2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := ws2.MemberConfigs["mia"]
	if got.Heartbeat == nil {
		t.Fatal("expected heartbeat to survive round-trip, got nil")
	}
	if !got.Heartbeat.Enabled {
		t.Error("expected Enabled=true after round-trip")
	}
	if got.Heartbeat.IntervalMinutes != 30 {
		t.Errorf("expected IntervalMinutes=30, got %d", got.Heartbeat.IntervalMinutes)
	}
	if got.Heartbeat.Body != "hello world" {
		t.Errorf("expected Body=%q, got %q", "hello world", got.Heartbeat.Body)
	}
	if got.Heartbeat.SessionID != "sess-123" {
		t.Errorf("expected SessionID=%q, got %q", "sess-123", got.Heartbeat.SessionID)
	}
}

// TestWorkspace_FreshWorkspace_NoMemberConfigs verifies that a freshly-decoded
// workspace with no member_configs field has nil MemberConfigs (deny-by-default).
func TestWorkspace_FreshWorkspace_NoMemberConfigs(t *testing.T) {
	raw := `{"id":"w1","name":"X","status":"active","created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z"}`
	var ws Workspace
	if err := json.Unmarshal([]byte(raw), &ws); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if ws.MemberConfigs != nil {
		t.Errorf("expected nil MemberConfigs for fresh workspace, got %v", ws.MemberConfigs)
	}
}
