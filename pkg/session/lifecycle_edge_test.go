// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order I-1 — round-trip coverage for the steered-by edge
// fields on LifecycleRecord (Origin, SteeredBy, Stop), through the REAL
// store: write, close (a fresh LifecycleStore instance simulates the
// process restart — LifecycleStore itself has no explicit Close, so a
// second instance rooted at the same directory is how this package's own
// existing tests, e.g. TestLifecycleParentIndex_WarmsAcrossSimulatedRestart,
// simulate "close and reopen"), reopen, read.

package session

import (
	"testing"
	"time"
)

// TestLifecycleRecord_SteeredByRoundTrip_AfterStoreReopen is CP-0's proof
// for I-1: a full SteeredBy edge, a populated Origin, and a Stop marker
// survive a Persist -> (simulated restart) -> Load unchanged.
func TestLifecycleRecord_SteeredByRoundTrip_AfterStoreReopen(t *testing.T) {
	dir := t.TempDir()
	first := NewLifecycleStore(dir)

	stopAt := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)
	rec := &LifecycleRecord{
		SessionID:      "child-edge-1",
		Generation:     1,
		State:          LifecycleRunning,
		OwnerScopeKind: OwnerScopeParentSession,
		OwnerScopeID:   "steerer-1",
		WorkspaceID:    "ws-1",
		AgentID:        "worker",
		Origin: &Origin{
			Kind:   OriginKindDelegate,
			CallID: "call-7",
		},
		SteeredBy: &SteeredBy{
			SteeringSessionID: "steerer-1",
			RootSessionID:     "root-1",
			ReportingTarget: ReportingTarget{
				SessionID: "steerer-1",
				Channel:   "webchat",
				ChatID:    "chat-1",
			},
			Authorization: Authorization{
				Mode:           AuthorizationModeDirect,
				RemainingDepth: 3,
			},
			Limits:         Limits{TimeoutSeconds: 600},
			ToolExclusions: []string{"switch_agent"},
		},
		Stop: &Stop{
			At:         stopAt,
			Generation: 1,
			By:         Principal{Kind: PrincipalKindHuman, ID: "dan"},
		},
	}
	if err := first.Persist(rec); err != nil {
		t.Fatalf("Persist: %v", err)
	}

	// Simulate a process restart: a fresh LifecycleStore instance rooted at
	// the SAME directory, with no in-memory state carried over.
	second := NewLifecycleStore(dir)
	loaded, err := second.Load("child-edge-1")
	if err != nil {
		t.Fatalf("Load after reopen: %v", err)
	}

	if loaded.Origin == nil {
		t.Fatal("Origin = nil after reopen, want the persisted value")
	}
	if loaded.Origin.Kind != OriginKindDelegate || loaded.Origin.CallID != "call-7" {
		t.Errorf("Origin = %+v, want {Kind: delegate, CallID: call-7}", loaded.Origin)
	}
	if loaded.Origin.TaskID != "" {
		t.Errorf("Origin.TaskID = %q, want empty (never set)", loaded.Origin.TaskID)
	}

	if loaded.SteeredBy == nil {
		t.Fatal("SteeredBy = nil after reopen, want the persisted edge")
	}
	sb := loaded.SteeredBy
	if sb.SteeringSessionID != "steerer-1" {
		t.Errorf("SteeredBy.SteeringSessionID = %q, want steerer-1", sb.SteeringSessionID)
	}
	if sb.RootSessionID != "root-1" {
		t.Errorf("SteeredBy.RootSessionID = %q, want root-1", sb.RootSessionID)
	}
	if sb.ReportingTarget != (ReportingTarget{SessionID: "steerer-1", Channel: "webchat", ChatID: "chat-1"}) {
		t.Errorf("SteeredBy.ReportingTarget = %+v, want {steerer-1 webchat chat-1}", sb.ReportingTarget)
	}
	if sb.Authorization != (Authorization{Mode: AuthorizationModeDirect, RemainingDepth: 3}) {
		t.Errorf("SteeredBy.Authorization = %+v, want {direct 3}", sb.Authorization)
	}
	if sb.Limits != (Limits{TimeoutSeconds: 600}) {
		t.Errorf("SteeredBy.Limits = %+v, want {600}", sb.Limits)
	}
	if len(sb.ToolExclusions) != 1 || sb.ToolExclusions[0] != "switch_agent" {
		t.Errorf("SteeredBy.ToolExclusions = %v, want [switch_agent]", sb.ToolExclusions)
	}

	if loaded.Stop == nil {
		t.Fatal("Stop = nil after reopen, want the persisted marker")
	}
	if !loaded.Stop.At.Equal(stopAt) {
		t.Errorf("Stop.At = %v, want %v", loaded.Stop.At, stopAt)
	}
	if loaded.Stop.Generation != 1 {
		t.Errorf("Stop.Generation = %d, want 1", loaded.Stop.Generation)
	}
	if loaded.Stop.By != (Principal{Kind: PrincipalKindHuman, ID: "dan"}) {
		t.Errorf("Stop.By = %+v, want {human dan}", loaded.Stop.By)
	}
}

// TestLifecycleRecord_OrdinaryRoot_HasNilSteeredByAndStop proves the "nobody
// steers this session" and "never stopped" cases serialize as absent
// (omitempty), not as zero-valued structs — I-1's "SteeredBy nil for a
// session nobody steers" / "Stop nil means no Stop has been stamped".
func TestLifecycleRecord_OrdinaryRoot_HasNilSteeredByAndStop(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := &LifecycleRecord{
		SessionID:      "root-edge-1",
		Generation:     1,
		State:          LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "ws-1",
		AgentID:        "jim",
		Origin:         &Origin{Kind: OriginKindChat},
	}
	if err := s.Persist(rec); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	loaded, err := s.Load("root-edge-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.SteeredBy != nil {
		t.Errorf("SteeredBy = %+v, want nil for an ordinary_root", loaded.SteeredBy)
	}
	if loaded.Stop != nil {
		t.Errorf("Stop = %+v, want nil (never stopped)", loaded.Stop)
	}
	if loaded.Origin == nil || loaded.Origin.Kind != OriginKindChat {
		t.Errorf("Origin = %+v, want {Kind: chat}", loaded.Origin)
	}
}

// TestLifecycleRecord_LegacyRecord_HasNilOrigin proves a record written
// before ADR-091 (no Origin field in the JSON at all) decodes with a nil
// Origin rather than a zero-valued one — the exact signal I-8's classifier
// uses to recognise a legacy_delegate record.
func TestLifecycleRecord_LegacyRecord_HasNilOrigin(t *testing.T) {
	s := newTestLifecycleStore(t)
	// Persist via the store so CreatedAt/UpdatedAt etc. are stamped
	// normally, deliberately leaving Origin/SteeredBy/Stop unset — exactly
	// what a pre-ADR-091 writer produced.
	rec := &LifecycleRecord{
		SessionID:      "legacy-edge-1",
		Generation:     0,
		State:          LifecycleRunning,
		OwnerScopeKind: OwnerScopeParentSession,
		OwnerScopeID:   "parent-1",
		WorkspaceID:    "ws-1",
		AgentID:        "worker",
	}
	if err := s.Persist(rec); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	loaded, err := s.Load("legacy-edge-1")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if loaded.Origin != nil {
		t.Errorf("Origin = %+v, want nil for a record that never set it", loaded.Origin)
	}
}
