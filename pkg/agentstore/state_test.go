package agentstore

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
)

func TestReadStateRevisionChangesWithEntityOrSoul(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	if err := s.Create("ava", &config.AgentConfig{Name: "Ava"}); err != nil {
		t.Fatal(err)
	}
	soulPath := filepath.Join(home, "agents", "ava", "SOUL.md")
	if err := os.MkdirAll(filepath.Dir(soulPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(soulPath, []byte("first"), 0o600); err != nil {
		t.Fatal(err)
	}

	one, err := s.ReadState("ava")
	if err != nil {
		t.Fatal(err)
	}
	if len(one.Revision) != 64 {
		t.Fatalf("revision length=%d want 64", len(one.Revision))
	}
	two, err := s.ReadState("ava")
	if err != nil {
		t.Fatal(err)
	}
	if two.Revision != one.Revision {
		t.Fatalf("stable bytes changed revision: %s != %s", two.Revision, one.Revision)
	}

	if err := os.WriteFile(soulPath, []byte("second"), 0o600); err != nil {
		t.Fatal(err)
	}
	three, err := s.ReadState("ava")
	if err != nil {
		t.Fatal(err)
	}
	if three.Revision == one.Revision {
		t.Fatal("soul change did not change revision")
	}
}

func TestMutateStateRejectsMalformedAndStaleRevisionWithoutWrites(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	if err := s.Create("ava", &config.AgentConfig{Name: "Ava"}); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadState("ava")
	if err != nil {
		t.Fatal(err)
	}

	for _, revision := range []string{"", "not-a-revision", string(make([]byte, 64))} {
		_, err := s.MutateState("ava", revision, func(a *config.AgentConfig) error { a.Name = "Changed"; return nil }, nil)
		if !errors.Is(err, ErrInvalidRevision) {
			t.Fatalf("revision %q error=%v want ErrInvalidRevision", revision, err)
		}
	}
	stale := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	_, err = s.MutateState("ava", stale, func(a *config.AgentConfig) error { a.Name = "Changed"; return nil }, nil)
	if !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale error=%v want ErrRevisionConflict", err)
	}
	after, err := s.ReadState("ava")
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Agent.Name != "Ava" {
		t.Fatalf("rejection wrote state: %#v", after)
	}
}

func TestMutateStateStagesBothAndReportsSoulReplacementPartial(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	if err := s.Create("judge", &config.AgentConfig{Name: "Judge"}); err != nil {
		t.Fatal(err)
	}
	soulPath := filepath.Join(home, "agents", "judge", "SOUL.md")
	if err := os.MkdirAll(filepath.Dir(soulPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(soulPath, []byte("old soul"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadState("judge")
	if err != nil {
		t.Fatal(err)
	}

	realReplace := s.replaceFile
	s.replaceFile = func(staged, target string) error {
		if target == soulPath {
			return errors.New("injected soul replace failure")
		}
		return realReplace(staged, target)
	}
	newSoul := "new soul"
	result, err := s.MutateState("judge", before.Revision, func(a *config.AgentConfig) error {
		a.Model = &config.AgentModelConfig{Primary: "new-model"}
		return nil
	}, &newSoul)
	if err == nil {
		t.Fatal("expected storage failure")
	}
	if result.PersistenceStatus != PersistencePartial || result.ActivationStatus != ActivationNotAttempted || result.ErrorStage != "replace_soul" {
		t.Fatalf("result=%+v", result)
	}
	if len(result.ChangedFields) != 1 || result.ChangedFields[0] != "entity" {
		t.Fatalf("changed=%v want [entity]", result.ChangedFields)
	}
	after, readErr := s.ReadState("judge")
	if readErr != nil {
		t.Fatal(readErr)
	}
	if after.Agent.Model == nil || after.Agent.Model.Primary != "new-model" {
		t.Fatalf("entity was not replaced: %#v", after.Agent.Model)
	}
	if after.Soul != "old soul" {
		t.Fatalf("soul=%q want old soul", after.Soul)
	}
	if result.Revision != after.Revision || result.Revision == before.Revision {
		t.Fatalf("partial revision=%q actual=%q before=%q", result.Revision, after.Revision, before.Revision)
	}
}

func TestMutateStateCompletesEntityAndSoulBeforeNotifying(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	if err := s.Create("judge", &config.AgentConfig{Name: "Judge"}); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, "agents", "judge"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "agents", "judge", "SOUL.md"), []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadState("judge")
	if err != nil {
		t.Fatal(err)
	}
	n := newFakeNotifier()
	s.SetNotifier(n)
	newSoul := "new"
	result, err := s.MutateState("judge", before.Revision, func(a *config.AgentConfig) error { a.Description = "changed"; return nil }, &newSoul)
	if err != nil {
		t.Fatal(err)
	}
	if result.PersistenceStatus != PersistenceComplete || result.ActivationStatus != ActivationNotAttempted {
		t.Fatalf("result=%+v", result)
	}
	if got := n.upserted["judge"]; got == nil || got.Description != "changed" {
		t.Fatalf("notifier=%#v", got)
	}
	after, err := s.ReadState("judge")
	if err != nil {
		t.Fatal(err)
	}
	if after.Soul != "new" || after.Agent.Description != "changed" || after.Revision != result.Revision {
		t.Fatalf("state=%+v result=%+v", after, result)
	}
}

func TestDeleteStateRejectsStaleRevisionWithoutDeleting(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	if err := s.Create("agent-1", &config.AgentConfig{Name: "before"}); err != nil {
		t.Fatal(err)
	}
	stale, err := s.ReadState("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.MutateState("agent-1", stale.Revision, func(a *config.AgentConfig) error {
		a.Name = "newer"
		return nil
	}, nil); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteState("agent-1", stale.Revision); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("error=%v want revision conflict", err)
	}
	got, err := s.Get("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "newer" {
		t.Fatalf("name=%q", got.Name)
	}
}

func TestCreateStateStagesEntityAndSoulBeforePublishing(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	soulPath := filepath.Join(home, "agents", "new-agent", "SOUL.md")
	realReplace := s.replaceFile
	s.replaceFile = func(staged, target string) error {
		if target == soulPath {
			return errors.New("injected soul replace failure")
		}
		return realReplace(staged, target)
	}
	result, err := s.CreateState("new-agent", &config.AgentConfig{Name: "New"}, "soul")
	if err == nil || result.PersistenceStatus != PersistencePartial || result.ErrorStage != "replace_soul" {
		t.Fatalf("result=%+v error=%v", result, err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "entities", "agents", "new-agent.json")); statErr != nil {
		t.Fatalf("entity not published before partial failure: %v", statErr)
	}
}

func TestCreateStatePersistsReadableCompositeState(t *testing.T) {
	s := New(t.TempDir())
	result, err := s.CreateState("new-agent", &config.AgentConfig{Name: "New"}, "soul")
	if err != nil {
		t.Fatal(err)
	}
	if result.PersistenceStatus != PersistenceComplete || result.Revision == "" {
		t.Fatalf("result=%+v", result)
	}
	state, err := s.ReadState("new-agent")
	if err != nil || state.Soul != "soul" || state.Revision != result.Revision {
		t.Fatalf("state=%+v err=%v", state, err)
	}
}
