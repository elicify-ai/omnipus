// Omnipus — agentstore partial-delete truthfulness tests (ADR-090 security
// review SE-C regression).
//
// Expectations derive from docs/internal/specs/adr-090-agent-configuration-
// and-skills-spec.md FR-007: after a partial failure "the reported pair is
// actual partial state" and "Repair re-reads actual entity/soul" — so a
// partial delete whose entity record is already gone must not report a
// revision, because ReadState (and get_agent above it) returns not-found and
// no read path can ever echo that value. ADR-090's working agreements add
// "read back what succeeded before proposing any remaining work" — an
// envelope that misreports readability sends that repair flow into a 404.
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package agentstore

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/entity"
)

// TestDeleteState_SoulRemovalFailureReportsNoReadableRevision is the SE-C
// regression. When the entity record is removed but the SOUL file is not,
// the mutation result must not present a revision: ReadState fails with
// not-found from that moment, so any revision value would claim a
// readability no read path can honor. The error must say so, the surviving
// disk state must be reported exactly (entity gone, soul intact), and the
// failure must not be mistakable for a revision conflict.
func TestDeleteState_SoulRemovalFailureReportsNoReadableRevision(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	if _, err := s.CreateState("agent-1", &config.AgentConfig{Name: "Agent One"}, "sensitive soul"); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadState("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	entityPath := filepath.Join(home, "entities", "agents", "agent-1.json")
	soulPath := filepath.Join(home, "agents", "agent-1", "SOUL.md")
	realRemove := s.removeFile
	s.removeFile = func(path string) error {
		if path == soulPath {
			return errors.New("injected soul remove failure")
		}
		return realRemove(path)
	}

	result, err := s.DeleteState("agent-1", before.Revision)
	if err == nil || !strings.Contains(err.Error(), "injected soul remove failure") {
		t.Fatalf("error=%v, want the injected soul remove failure", err)
	}
	if errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("partial delete misreported as a revision conflict: %v", err)
	}
	if result.PersistenceStatus != PersistencePartial || result.ErrorStage != "remove_soul" {
		t.Fatalf("result=%+v, want partial persistence at remove_soul", result)
	}
	if want := []string{"entity"}; !reflect.DeepEqual(result.ChangedFields, want) {
		t.Fatalf("changed_fields=%v, want %v (the entity removal did happen)", result.ChangedFields, want)
	}
	if result.Revision != "" {
		t.Fatalf("revision=%q, want empty — the entity record is gone, so ReadState reports not-found and no read path can echo a revision", result.Revision)
	}
	if !strings.Contains(err.Error(), "not-found") || !strings.Contains(err.Error(), "no readable revision") {
		t.Fatalf("error must state that reads now report not-found and no readable revision exists: %v", err)
	}

	// Readback: the read model confirms the envelope's claim.
	if _, readErr := s.ReadState("agent-1"); !errors.Is(readErr, entity.ErrNotFound) {
		t.Fatalf("ReadState error=%v, want entity.ErrNotFound", readErr)
	}
	if _, statErr := os.Stat(entityPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("entity stat error=%v, want not exist", statErr)
	}
	got, readErr := os.ReadFile(soulPath)
	if readErr != nil || string(got) != "sensitive soul" {
		t.Fatalf("soul=%q error=%v, want the untouched original bytes", got, readErr)
	}
}

// TestDeleteState_EntityRemovalFailureLeavesBothFilesUnchanged proves the
// zero-write contract at the file level: when the FIRST removal fails,
// nothing was deleted, both files keep their original bytes, and the
// reported revision is the still-readable pre-delete revision — the one case
// where a failure revision is truthful, because ReadState still returns it.
func TestDeleteState_EntityRemovalFailureLeavesBothFilesUnchanged(t *testing.T) {
	home := t.TempDir()
	s := New(home)
	if _, err := s.CreateState("agent-1", &config.AgentConfig{Name: "Agent One"}, "sensitive soul"); err != nil {
		t.Fatal(err)
	}
	before, err := s.ReadState("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	entityPath := filepath.Join(home, "entities", "agents", "agent-1.json")
	soulPath := filepath.Join(home, "agents", "agent-1", "SOUL.md")
	entityBefore, err := os.ReadFile(entityPath)
	if err != nil {
		t.Fatal(err)
	}
	realRemove := s.removeFile
	s.removeFile = func(path string) error {
		if path == entityPath {
			return errors.New("injected entity remove failure")
		}
		return realRemove(path)
	}

	result, err := s.DeleteState("agent-1", before.Revision)
	if err == nil || !strings.Contains(err.Error(), "injected entity remove failure") {
		t.Fatalf("error=%v, want the injected entity remove failure", err)
	}
	if result.PersistenceStatus != PersistenceNone || result.ErrorStage != "remove_entity" || len(result.ChangedFields) != 0 {
		t.Fatalf("result=%+v, want a zero-write failure at remove_entity", result)
	}
	if result.Revision != before.Revision {
		t.Fatalf("revision=%q, want the still-readable pre-delete revision %q", result.Revision, before.Revision)
	}

	// Readback: both files byte-identical, and the read model still echoes
	// the reported revision.
	entityAfter, err := os.ReadFile(entityPath)
	if err != nil || string(entityAfter) != string(entityBefore) {
		t.Fatalf("entity record changed on a zero-write failure: %q (error=%v)", entityAfter, err)
	}
	soulAfter, err := os.ReadFile(soulPath)
	if err != nil || string(soulAfter) != "sensitive soul" {
		t.Fatalf("soul=%q error=%v, want untouched bytes", soulAfter, err)
	}
	after, err := s.ReadState("agent-1")
	if err != nil {
		t.Fatal(err)
	}
	if after.Revision != before.Revision || after.Soul != "sensitive soul" {
		t.Fatalf("state drifted on a zero-write failure: revision=%q soul=%q", after.Revision, after.Soul)
	}
}
