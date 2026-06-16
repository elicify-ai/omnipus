package taskstore

import (
	"errors"
	"testing"
)

// newTask is a tiny helper to build a queued task entity with an explicit ID
// and optional parent, used to construct deterministic graphs in cycle tests.
func newTask(id, parent string) *TaskEntity {
	return &TaskEntity{
		ID:           id,
		Title:        "t-" + id,
		Prompt:       "p",
		AgentID:      "agent-1",
		ParentTaskID: parent,
		Priority:     3,
		Status:       "queued",
		TriggerType:  "manual",
		CreatedBy:    "user",
	}
}

func TestCreate_RejectsSelfParent(t *testing.T) {
	s := New(t.TempDir())
	err := s.Create(newTask("a", "a"))
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("self-parent must be rejected with ErrCycle, got %v", err)
	}
}

func TestCreate_RejectsMissingParent(t *testing.T) {
	s := New(t.TempDir())
	// Parent "ghost" does not exist — must be rejected, not silently accepted.
	err := s.Create(newTask("a", "ghost"))
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing parent must be rejected with ErrNotFound, got %v", err)
	}
}

// TestCreate_RejectsTwoNodeCycle builds A (root) and B (parent=A), then attempts
// to re-create A with parent=B. The edge A→B closes the loop A→B→A and must be
// rejected.
func TestCreate_RejectsTwoNodeCycle(t *testing.T) {
	s := New(t.TempDir())

	if err := s.Create(newTask("a", "")); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := s.Create(newTask("b", "a")); err != nil {
		t.Fatalf("create B(parent=A): %v", err)
	}

	// Now try to give A the parent B: A→B, but B→A already exists ⇒ cycle.
	err := s.Create(newTask("a", "b"))
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("2-node cycle A→B→A must be rejected with ErrCycle, got %v", err)
	}
}

// TestCreate_RejectsThreeNodeCycle builds the chain A→nil, B→A, C→B, then
// attempts to re-create A with parent=C. The edge A→C closes the loop
// A→C→B→A and must be rejected.
func TestCreate_RejectsThreeNodeCycle(t *testing.T) {
	s := New(t.TempDir())

	if err := s.Create(newTask("a", "")); err != nil {
		t.Fatalf("create A: %v", err)
	}
	if err := s.Create(newTask("b", "a")); err != nil {
		t.Fatalf("create B(parent=A): %v", err)
	}
	if err := s.Create(newTask("c", "b")); err != nil {
		t.Fatalf("create C(parent=B): %v", err)
	}

	// A→C would close A→C→B→A.
	err := s.Create(newTask("a", "c"))
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("3-node cycle A→C→B→A must be rejected with ErrCycle, got %v", err)
	}
}

// TestCreate_AcceptsValidChain confirms a legal DAG chain (no cycle) is accepted
// up to the depth bound, so the cycle check does not over-reject.
func TestCreate_AcceptsValidChain(t *testing.T) {
	s := New(t.TempDir())
	prev := ""
	for _, id := range []string{"n0", "n1", "n2", "n3"} {
		if err := s.Create(newTask(id, prev)); err != nil {
			t.Fatalf("create %q (parent=%q): %v", id, prev, err)
		}
		prev = id
	}
}

// TestCreate_RejectsOverDepthChain confirms a chain deeper than maxParentDepth
// is rejected with ErrCycle (the depth backstop).
func TestCreate_RejectsOverDepthChain(t *testing.T) {
	s := New(t.TempDir())
	prev := ""
	// Build a legal chain of maxParentDepth+1 ancestor nodes. A new task pointing
	// at the tip then has more than maxParentDepth ancestors, exceeding the bound.
	for i := 0; i <= maxParentDepth; i++ {
		id := "d" + string(rune('a'+i))
		if err := s.Create(newTask(id, prev)); err != nil {
			t.Fatalf("create %q (parent=%q): %v", id, prev, err)
		}
		prev = id
	}
	// Pointing at the tip walks maxParentDepth+1 ancestors ⇒ exceeds the bound.
	err := s.Create(newTask("too-deep", prev))
	if !errors.Is(err, ErrCycle) {
		t.Fatalf("over-depth chain must be rejected with ErrCycle, got %v", err)
	}
}
