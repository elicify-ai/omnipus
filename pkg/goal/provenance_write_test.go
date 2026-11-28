package goal

import (
	"errors"
	"reflect"
	"testing"
	"time"

	generated "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/task"
)

// I-2 / S-111 (ruling-check-delta "S-111 writers"): Store.Update takes an
// explicit Write. A write with only Mutate leaves Goal.Provenance unchanged; a
// write with Provenance joins it after the mutation, in the same write; a
// failed mutation joins nothing.
func TestGoalProvenance_UpdateJoinsOnlyAnExplicitProvenance(t *testing.T) {
	s := NewStore(t.TempDir())
	criterion := task.AcceptanceCriterion{
		Kind: task.KindProse, Judgment: task.JudgmentBoolean, Text: "the tests pass",
		Author: task.CriterionAuthor{Kind: task.AuthorKindUser, ID: "s111"}, Status: task.CritPending,
	}
	g, err := New(generated.GoalOwnerKindSession, "s111-session", generated.GoalSourceChatCompiled,
		"S-111 goal", "", []task.AcceptanceCriterion{criterion}, []task.AcceptanceCriterion{criterion}, 20, time.Now().UTC())
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	g.Provenance = []string{"web"}
	if cerr := s.Create(g); cerr != nil {
		t.Fatalf("Create: %v", cerr)
	}

	// Mutate only: a state/counter change keeps the recorded provenance.
	updated, err := s.Update(g.GoalID, Write{Mutate: func(cur *Goal) error {
		cur.ZeroOutputPushes = 3
		return nil
	}})
	if err != nil {
		t.Fatalf("Update (mutate only): %v", err)
	}
	if updated.ZeroOutputPushes != 3 {
		t.Fatalf("instrument: the mutation did not apply")
	}
	if want := []string{"web"}; !reflect.DeepEqual(updated.Provenance, want) {
		t.Fatalf("a mutate-only write changed provenance to %v, want unchanged %v", updated.Provenance, want)
	}

	// A failed mutation writes nothing, provenance included.
	telegram := []string{"connector:telegram"}
	boom := errors.New("refused")
	if _, ferr := s.Update(g.GoalID, Write{Provenance: &telegram, Mutate: func(*Goal) error { return boom }}); !errors.Is(ferr, boom) {
		t.Fatalf("Update with a failing mutation: err = %v, want %v", ferr, boom)
	}
	got, err := s.Get(g.GoalID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if want := []string{"web"}; !reflect.DeepEqual(got.Provenance, want) {
		t.Fatalf("a refused write changed provenance to %v, want %v", got.Provenance, want)
	}

	// An explicit provenance is joined in the same write.
	joined, err := s.Update(g.GoalID, Write{Provenance: &telegram, Mutate: func(cur *Goal) error {
		cur.Definition = "changed by an agent"
		return nil
	}})
	if err != nil {
		t.Fatalf("Update (join): %v", err)
	}
	want := []string{"connector:telegram", "web"}
	if !reflect.DeepEqual(joined.Provenance, want) {
		t.Fatalf("joined provenance = %v, want %v", joined.Provenance, want)
	}
	if got, _ := s.Get(g.GoalID); got == nil || !reflect.DeepEqual(got.Provenance, want) {
		t.Fatalf("persisted provenance = %v, want %v", got, want)
	}

	// A write with no mutation is refused, not silently accepted.
	if _, err := s.Update(g.GoalID, Write{Provenance: &telegram}); err == nil {
		t.Fatalf("Update without Mutate must be refused")
	}
}
