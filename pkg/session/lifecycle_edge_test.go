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
	"fmt"
	"strings"
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
		Generation:     1,
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

// --- persistLocked choke-point validation for the four ADR-091 I-1 fields ---
//
// A type-design review found every invariant on Generation/Origin/SteeredBy/
// Stop enforced ONLY at the single caller SteerLauncher.Launch
// (pkg/agent/steer_launcher.go), never at LifecycleStore.persistLocked — the
// one choke point both Persist and Mutate fund into. Each subtest below
// proves a shape the review named as currently writing clean JSONL is now
// rejected, with a clear, field-naming error.

// baseValidLifecycleRecord returns a record that passes every OTHER
// persistLocked check (state, needs_input pairing, failed_reason pairing,
// owner_scope_kind) so each subtest below isolates exactly the ADR-091 I-1
// check it is proving.
func baseValidLifecycleRecord(sessionID string) *LifecycleRecord {
	return &LifecycleRecord{
		SessionID:      sessionID,
		Generation:     1,
		State:          LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "ws-1",
		AgentID:        "agent-1",
	}
}

func TestLifecycleStore_Persist_RejectsGenerationBelowOne(t *testing.T) {
	for _, gen := range []int{0, -1, -99} {
		t.Run(fmt.Sprintf("generation_%d", gen), func(t *testing.T) {
			s := newTestLifecycleStore(t)
			rec := baseValidLifecycleRecord("gen-bad")
			rec.Generation = gen
			err := s.Persist(rec)
			if err == nil {
				t.Fatalf("Persist(generation=%d) succeeded, want rejection — generation starts at 1", gen)
			}
			if !strings.Contains(err.Error(), "generation must be >= 1") {
				t.Errorf("Persist(generation=%d) error = %v, want a generation>=1 message", gen, err)
			}
			if s.Exists("gen-bad") {
				t.Error("a rejected record must not land on disk")
			}
		})
	}
}

func TestLifecycleStore_Persist_AcceptsGenerationOne(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := baseValidLifecycleRecord("gen-good")
	if err := s.Persist(rec); err != nil {
		t.Fatalf("Persist(generation=1) failed: %v", err)
	}
}

func TestLifecycleStore_Persist_RejectsUnknownOriginKind(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := baseValidLifecycleRecord("origin-bad-kind")
	rec.Origin = &Origin{Kind: OriginKind("nonsense")}
	err := s.Persist(rec)
	if err == nil {
		t.Fatal("Persist(origin.kind=nonsense) succeeded, want rejection — IsValidOriginKind must gate persistLocked")
	}
	if !strings.Contains(err.Error(), "invalid origin kind") {
		t.Errorf("Persist(origin.kind=nonsense) error = %v, want an invalid-origin-kind message", err)
	}
	if s.Exists("origin-bad-kind") {
		t.Error("a rejected record must not land on disk")
	}
}

func TestLifecycleStore_Persist_RejectsTaskOriginWithoutTaskID(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := baseValidLifecycleRecord("origin-task-no-id")
	rec.Origin = &Origin{Kind: OriginKindTask, TaskID: ""}
	err := s.Persist(rec)
	if err == nil {
		t.Fatal("Persist(origin.kind=task, task_id=\"\") succeeded, want rejection — the exact shape SteerLauncher.Launch already refuses with steer.ErrTaskIDRequired")
	}
	if !strings.Contains(err.Error(), "requires origin.task_id") {
		t.Errorf("Persist(origin.kind=task, task_id=\"\") error = %v, want a task_id-required message", err)
	}
	if s.Exists("origin-task-no-id") {
		t.Error("a rejected record must not land on disk")
	}
}

func TestLifecycleStore_Persist_AcceptsTaskOriginWithTaskID(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := baseValidLifecycleRecord("origin-task-with-id")
	rec.Origin = &Origin{Kind: OriginKindTask, TaskID: "task-123"}
	if err := s.Persist(rec); err != nil {
		t.Fatalf("Persist(origin.kind=task, task_id=set) failed: %v", err)
	}
}

func TestLifecycleStore_Persist_RejectsSteeredByMissingSteeringSessionID(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := baseValidLifecycleRecord("steeredby-no-steering")
	rec.OwnerScopeKind = OwnerScopeParentSession
	rec.OwnerScopeID = "parent-1"
	rec.SteeredBy = &SteeredBy{SteeringSessionID: "", RootSessionID: "root-1"}
	err := s.Persist(rec)
	if err == nil {
		t.Fatal("Persist(steered_by.steering_session_id=\"\") succeeded, want rejection — parentIndex.add(\"\", id) would silently no-op, leaving an unindexed orphan")
	}
	if !strings.Contains(err.Error(), "non-empty steering_session_id") {
		t.Errorf("Persist(steered_by.steering_session_id=\"\") error = %v, want a steering_session_id-required message", err)
	}
	if s.Exists("steeredby-no-steering") {
		t.Error("a rejected record must not land on disk")
	}
}

func TestLifecycleStore_Persist_RejectsSteeredByMissingRootSessionID(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := baseValidLifecycleRecord("steeredby-no-root")
	rec.OwnerScopeKind = OwnerScopeParentSession
	rec.OwnerScopeID = "parent-1"
	rec.SteeredBy = &SteeredBy{SteeringSessionID: "parent-1", RootSessionID: ""}
	err := s.Persist(rec)
	if err == nil {
		t.Fatal("Persist(steered_by.root_session_id=\"\") succeeded, want rejection")
	}
	if !strings.Contains(err.Error(), "non-empty root_session_id") {
		t.Errorf("Persist(steered_by.root_session_id=\"\") error = %v, want a root_session_id-required message", err)
	}
	if s.Exists("steeredby-no-root") {
		t.Error("a rejected record must not land on disk")
	}
}

func TestLifecycleStore_Persist_AcceptsFullSteeredBy(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := baseValidLifecycleRecord("steeredby-good")
	rec.OwnerScopeKind = OwnerScopeParentSession
	rec.OwnerScopeID = "parent-1"
	rec.SteeredBy = &SteeredBy{SteeringSessionID: "parent-1", RootSessionID: "root-1"}
	if err := s.Persist(rec); err != nil {
		t.Fatalf("Persist(steered_by=full) failed: %v", err)
	}
}

// TestLifecycleStore_Persist_RejectsStopGenerationAheadOfRecord proves the
// D8 tri-state's unreachable-by-design fourth shape (Stop.Generation >
// Generation — naming a generation that has not happened yet) is refused at
// the write choke point, not merely left "silently reads as not stopped" by
// callers.
func TestLifecycleStore_Persist_RejectsStopGenerationAheadOfRecord(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := baseValidLifecycleRecord("stop-ahead")
	rec.Generation = 1
	rec.Stop = &Stop{At: time.Now().UTC(), Generation: 99, By: Principal{Kind: PrincipalKindHuman, ID: "dan"}}
	err := s.Persist(rec)
	if err == nil {
		t.Fatal("Persist(stop.generation=99 on generation=1) succeeded, want rejection")
	}
	if !strings.Contains(err.Error(), "must not exceed record generation") {
		t.Errorf("Persist(stop.generation=99 on generation=1) error = %v, want a stop-generation-exceeds message", err)
	}
	if s.Exists("stop-ahead") {
		t.Error("a rejected record must not land on disk")
	}
}

// TestLifecycleStore_Persist_RejectsTerminalRecordWithCurrentGenerationStop
// proves a record cannot claim BOTH "finished on its own" (Terminal()) AND
// "stopped right now" (Stop.Generation == Generation) — the two live
// production writers (steer_completion.go, steer_cancel.go) each refuse to
// produce this combination themselves; persistLocked now backstops that at
// the one choke point every writer funnels through.
func TestLifecycleStore_Persist_RejectsTerminalRecordWithCurrentGenerationStop(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := baseValidLifecycleRecord("terminal-and-stopped")
	rec.State = LifecycleCompleted
	rec.Stop = &Stop{At: time.Now().UTC(), Generation: 1, By: Principal{Kind: PrincipalKindHuman, ID: "dan"}}
	err := s.Persist(rec)
	if err == nil {
		t.Fatal("Persist(terminal + current-generation stop) succeeded, want rejection")
	}
	if !strings.Contains(err.Error(), "cannot carry a current-generation stop marker") {
		t.Errorf("Persist(terminal + current-generation stop) error = %v, want a terminal-cannot-carry-stop message", err)
	}
	if s.Exists("terminal-and-stopped") {
		t.Error("a rejected record must not land on disk")
	}
}

// TestLifecycleStore_Persist_AcceptsTerminalRecordWithPriorGenerationStop
// proves the ADJACENT, legitimate shape survives: a Stop stamped on an
// EARLIER generation than the one that went terminal (I-6 Revive's inert
// history) is not confused with "stopped now."
func TestLifecycleStore_Persist_AcceptsTerminalRecordWithPriorGenerationStop(t *testing.T) {
	s := newTestLifecycleStore(t)
	rec := baseValidLifecycleRecord("terminal-with-old-stop")
	rec.Generation = 2
	rec.State = LifecycleCompleted
	rec.Stop = &Stop{At: time.Now().UTC(), Generation: 1, By: Principal{Kind: PrincipalKindHuman, ID: "dan"}}
	if err := s.Persist(rec); err != nil {
		t.Fatalf("Persist(terminal + prior-generation stop) failed: %v", err)
	}
}

// --- LifecycleRecord.Stopped() — the tri-state named once ---

func TestLifecycleRecord_Stopped(t *testing.T) {
	tests := []struct {
		name       string
		stop       *Stop
		generation int
		want       bool
	}{
		{"nil_stop_never_stopped", nil, 1, false},
		{"stop_generation_equals_record_generation_live", &Stop{Generation: 3}, 3, true},
		{"stop_generation_below_record_generation_inert_history_after_revive", &Stop{Generation: 1}, 2, false},
		{"stop_generation_above_record_generation_unreachable_by_design", &Stop{Generation: 5}, 3, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &LifecycleRecord{Generation: tt.generation, Stop: tt.stop}
			if got := rec.Stopped(); got != tt.want {
				t.Errorf("Stopped() = %v, want %v (stop=%+v, generation=%d)", got, tt.want, tt.stop, tt.generation)
			}
		})
	}
}

func TestLifecycleRecord_Stopped_NilReceiver(t *testing.T) {
	var rec *LifecycleRecord
	if rec.Stopped() {
		t.Error("Stopped() on a nil *LifecycleRecord must be false, not panic")
	}
}
