package taskstore

import (
	"errors"
	"strings"
	"testing"
)

// newBlockedTask builds a queued task with an explicit ID and a blocked_by list,
// used to construct deterministic dependency graphs in blocked_by cycle tests.
func newBlockedTask(id string, blockedBy []string) *TaskEntity {
	return &TaskEntity{
		ID:          id,
		Title:       "t-" + id,
		Prompt:      "p",
		AgentID:     "agent-1",
		BlockedBy:   blockedBy,
		Priority:    3,
		Status:      "queued",
		TriggerType: "manual",
		CreatedBy:   "user",
	}
}

// TestValidateBlockedBy_RejectsSelfEdge confirms the trivial A→A self-cycle is
// still rejected.
func TestValidateBlockedBy_RejectsSelfEdge(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Create(newBlockedTask("a", nil)); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := s.ValidateBlockedBy("a", []string{"a"}); err == nil {
		t.Fatal("self-edge A→A must be rejected, got nil")
	}
}

// TestValidateBlockedBy_RejectsTwoNodeCycle builds A and B(blocked_by=A), then
// validates giving A the dependency B. The edge A→B closes the loop A→B→A and
// must be rejected with ErrCycle.
func TestValidateBlockedBy_RejectsTwoNodeCycle(t *testing.T) {
	s := New(t.TempDir())

	if err := s.Create(newBlockedTask("a", nil)); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := s.Create(newBlockedTask("b", []string{"a"})); err != nil {
		t.Fatalf("create B(blocked_by=A): %v", err)
	}

	// A blocked_by B, but B blocked_by A already exists ⇒ 2-node cycle.
	err := s.ValidateBlockedBy("a", []string{"b"})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("2-node cycle A→B→A must be rejected with ErrCycle, got %v", err)
	}
}

// TestValidateBlockedBy_RejectsThreeNodeCycle builds A, B(blocked_by=A),
// C(blocked_by=B), then validates giving A the dependency C. The edge A→C
// closes the loop A→C→B→A and must be rejected with ErrCycle.
func TestValidateBlockedBy_RejectsThreeNodeCycle(t *testing.T) {
	s := New(t.TempDir())

	if err := s.Create(newBlockedTask("a", nil)); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := s.Create(newBlockedTask("b", []string{"a"})); err != nil {
		t.Fatalf("create B(blocked_by=A): %v", err)
	}
	if err := s.Create(newBlockedTask("c", []string{"b"})); err != nil {
		t.Fatalf("create C(blocked_by=B): %v", err)
	}

	err := s.ValidateBlockedBy("a", []string{"c"})
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("3-node cycle A→C→B→A must be rejected with ErrCycle, got %v", err)
	}
}

// TestValidateBlockedBy_AcceptsValidDAG confirms a legal dependency DAG (no
// cycle) is accepted, so the multi-hop check does not over-reject. D depends on
// B and C, both of which depend on A — a diamond, which is a valid DAG.
func TestValidateBlockedBy_AcceptsValidDAG(t *testing.T) {
	s := New(t.TempDir())

	if err := s.Create(newBlockedTask("a", nil)); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := s.Create(newBlockedTask("b", []string{"a"})); err != nil {
		t.Fatalf("create B(blocked_by=A): %v", err)
	}
	if err := s.Create(newBlockedTask("c", []string{"a"})); err != nil {
		t.Fatalf("create C(blocked_by=A): %v", err)
	}
	if err := s.Create(newBlockedTask("d", nil)); err != nil {
		t.Fatalf("create D: %v", err)
	}

	// D blocked_by [B, C] — a diamond over A. No cycle: must be accepted.
	if err := s.ValidateBlockedBy("d", []string{"b", "c"}); err != nil {
		t.Fatalf("valid diamond DAG D→{B,C}→A must be accepted, got %v", err)
	}
}

// TestValidateBlockedBy_AcceptsEmptySelfID confirms the Create path (selfID="")
// runs existence/self-edge checks but no cycle-through-self check (a not-yet-
// created task has no inbound edges).
func TestValidateBlockedBy_AcceptsEmptySelfID(t *testing.T) {
	s := New(t.TempDir())
	if err := s.Create(newBlockedTask("a", nil)); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := s.ValidateBlockedBy("", []string{"a"}); err != nil {
		t.Fatalf("new task blocked_by an existing task must be accepted, got %v", err)
	}
}

// TestValidateBlockedBy_RejectsMissingDependency confirms a reference to a
// non-existent task is still rejected.
func TestValidateBlockedBy_RejectsMissingDependency(t *testing.T) {
	s := New(t.TempDir())
	err := s.ValidateBlockedBy("a", []string{"ghost"})
	if err == nil {
		t.Fatal("missing dependency must be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Fatalf("missing dependency error should mention 'not found', got %v", err)
	}
}
