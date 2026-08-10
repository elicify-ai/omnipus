package task

import (
	"sync"
	"testing"
)

// TestNormalizeCriteria_DoesNotMutateCallerSlice pins the contract that
// removed a real data race.
//
// normalizeCriteria used to server-set IDs by writing through the caller's
// slice. That made the store scribble into memory the caller still owned, so
// two goroutines normalising slices that share a backing array raced — with no
// lock anywhere nearby to hint that they might. It was invisible because
// neither CI race gate covered a package that reaches this path.
//
// Every call site already assigns the returned slice, so nothing ever wanted
// the in-place behaviour.
func TestNormalizeCriteria_DoesNotMutateCallerSlice(t *testing.T) {
	input := []AcceptanceCriterion{
		{Kind: KindProse, Text: "does the thing",
			Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"}},
	}

	out, err := normalizeCriteria(input)
	if err != nil {
		t.Fatalf("normalizeCriteria() error = %v", err)
	}

	if input[0].ID != "" {
		t.Errorf("normalizeCriteria wrote a server-set ID into the CALLER's slice (%q) — "+
			"that is the in-place mutation whose racing writes this contract removes", input[0].ID)
	}
	if input[0].Status != "" {
		t.Errorf("normalizeCriteria defaulted Status in the CALLER's slice (%q)", input[0].Status)
	}
	if out[0].ID == "" {
		t.Error("the RETURNED slice must carry the server-set ID — copying must not skip the work")
	}
	if out[0].Status != CritPending {
		t.Errorf("returned Status = %q, want %q", out[0].Status, CritPending)
	}
}

// TestNormalizeCriteria_ConcurrentSharedBackingArray is the race regression
// itself: two goroutines normalising slices that share one backing array. It
// reports a data race under -race on the pre-fix code and passes on the fixed
// code. Without -race it still passes either way — which is exactly why the
// package has to be in a race gate, not merely in `go test`.
func TestNormalizeCriteria_ConcurrentSharedBackingArray(t *testing.T) {
	shared := []AcceptanceCriterion{
		{Kind: KindProse, Text: "a", Author: CriterionAuthor{Kind: AuthorKindUser, ID: "alice"}},
		{Kind: KindProse, Text: "b", Author: CriterionAuthor{Kind: AuthorKindAgent, ID: "jim"}},
	}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := normalizeCriteria(shared); err != nil {
				t.Errorf("normalizeCriteria() error = %v", err)
			}
		}()
	}
	wg.Wait()
}

// TestNormalizeCriteria_NilStaysNil guards the one behaviour a naive copy
// would change: callers distinguish a nil criteria slice from an empty one
// (pkg/task/store.go's Update nils out an empty result explicitly).
func TestNormalizeCriteria_NilStaysNil(t *testing.T) {
	out, err := normalizeCriteria(nil)
	if err != nil {
		t.Fatalf("normalizeCriteria(nil) error = %v", err)
	}
	if out != nil {
		t.Errorf("normalizeCriteria(nil) = %#v, want nil — callers treat nil and empty differently", out)
	}
}

// behaviourCriterion builds the ONE criterion kind that carries mutable
// pointer state. This is the input class the first version of this file
// missed: it exercised only KindProse, so it passed while a shallow copy left
// the race fully live. The pointer fields are exactly what makes a shallow
// copy insufficient, so they are exactly what has to be tested.
func behaviourCriterion(text string) AcceptanceCriterion {
	return AcceptanceCriterion{
		Kind: KindBehavior, Text: text,
		Author:   CriterionAuthor{Kind: AuthorKindUser, ID: "alice"},
		Behavior: &CriterionBehavior{Tool: "bash"},
	}
}

// TestNormalizeCriteria_DoesNotMutateCallerBehaviour is the deep-copy
// contract. validateCriterionBehavior server-sets MinCount and Scope by
// writing THROUGH the *CriterionBehavior pointer, so a shallow copy of the
// slice shares that pointer and the store still scribbles into the caller's
// memory — and two goroutines still race on it.
func TestNormalizeCriteria_DoesNotMutateCallerBehaviour(t *testing.T) {
	input := []AcceptanceCriterion{behaviourCriterion("does the thing")}

	out, err := normalizeCriteria(input)
	if err != nil {
		t.Fatalf("normalizeCriteria() error = %v", err)
	}

	if input[0].Behavior.MinCount != nil {
		t.Errorf("normalizeCriteria server-set MinCount=%d through the CALLER's Behavior pointer — "+
			"the slice copy is shallow, so the pointed-to struct is still shared",
			*input[0].Behavior.MinCount)
	}
	if input[0].Behavior.Scope != "" {
		t.Errorf("normalizeCriteria server-set Scope=%q through the CALLER's Behavior pointer",
			input[0].Behavior.Scope)
	}

	// The returned copy must still carry the normalization — deep-copying
	// must not skip the work.
	if out[0].Behavior == nil || out[0].Behavior.MinCount == nil || *out[0].Behavior.MinCount != 1 {
		t.Error("the RETURNED criterion must carry the defaulted MinCount")
	}
	if out[0].Behavior.Scope != BehaviorScopeTaskSession {
		t.Errorf("returned Scope = %q, want %q", out[0].Behavior.Scope, BehaviorScopeTaskSession)
	}
	if out[0].Behavior == input[0].Behavior {
		t.Error("the returned criterion shares the caller's Behavior pointer — that IS the race")
	}
}

// TestNormalizeCriteria_ConcurrentSharedBehaviour is the race regression for
// the pointer-state case. It reports DATA RACE under -race against a shallow
// copy; the prose-only version of this test does not, which is why the first
// pass shipped a fix that only worked for prose.
func TestNormalizeCriteria_ConcurrentSharedBehaviour(t *testing.T) {
	shared := []AcceptanceCriterion{behaviourCriterion("a"), behaviourCriterion("b")}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := normalizeCriteria(shared); err != nil {
				t.Errorf("normalizeCriteria() error = %v", err)
			}
		}()
	}
	wg.Wait()
}
