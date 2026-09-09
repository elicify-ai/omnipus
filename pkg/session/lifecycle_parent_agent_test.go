// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// FR-013/FR-015 — LifecycleRecord.ParentAgentID, the ONLY parent linkage on
// a durable lifecycle record and the sole legal predicate for "the subagents
// I started" (list_jobs).
//
// These tests assert OUTCOMES on disk and through the public filter, not the
// presence of a guard: that the key is always physically present in the
// JSONL (no `omitempty`), and that the filter matches ParentAgentID and
// NOTHING else — in particular not the parent↔child-SHARED ParentDurableKey.

package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// rawTailLine returns the last non-empty JSONL line persisted for sessionID,
// decoded into a generic key→value map. Reading the RAW file (rather than
// Load) is the point: it is the only way to distinguish "the key is present
// with an empty value" from "the key is absent", which Go's zero value makes
// indistinguishable after unmarshalling into the struct.
func rawTailLine(t *testing.T, s *LifecycleStore, sessionID string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(s.Dir(), sessionID+".jsonl"))
	if err != nil {
		t.Fatalf("read raw lifecycle file for %q: %v", sessionID, err)
	}
	var last string
	for _, line := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(line) != "" {
			last = line
		}
	}
	if last == "" {
		t.Fatalf("no non-empty JSONL line persisted for %q", sessionID)
	}
	out := map[string]json.RawMessage{}
	if err := json.Unmarshal([]byte(last), &out); err != nil {
		t.Fatalf("decode raw JSONL line for %q: %v\nline: %s", sessionID, err, last)
	}
	return out
}

// TestLifecycleRecord_ParentAgentIDRoundTrip proves both halves of FR-015's
// on-disk contract: a populated parent survives persist→load unchanged, and
// the `parent_agent_id` key is physically present in the JSONL even when the
// value is empty (i.e. the field carries no `omitempty`). The empty case is
// what makes an unattributable record a VISIBLE bug instead of a row
// byte-identical to one written before the field existed.
func TestLifecycleRecord_ParentAgentIDRoundTrip(t *testing.T) {
	t.Run("populated parent survives persist and reload", func(t *testing.T) {
		s := newTestLifecycleStore(t)
		if err := s.Persist(&LifecycleRecord{
			SessionID: "sess-parented", State: LifecycleQueued,
			OwnerScopeKind: OwnerScopeHuman,
			ParentAgentID:  "mia", ParentDurableKey: "transcript-1",
			WorkspaceID: "ws-1", AgentID: "ray",
		}); err != nil {
			t.Fatalf("persist: %v", err)
		}

		got, err := s.Load("sess-parented")
		if err != nil {
			t.Fatalf("load: %v", err)
		}
		if got.ParentAgentID != "mia" {
			t.Errorf("ParentAgentID = %q, want %q", got.ParentAgentID, "mia")
		}
		// The parent linkage must be its OWN value, not a copy of any
		// neighbouring field a future implementer might infer it from.
		if got.ParentAgentID == got.ParentDurableKey {
			t.Errorf("ParentAgentID (%q) must not equal ParentDurableKey (%q)", got.ParentAgentID, got.ParentDurableKey)
		}
		if got.ParentAgentID == got.AgentID {
			t.Errorf("ParentAgentID (%q) must not equal AgentID, which is the CHILD's id", got.ParentAgentID)
		}

		raw := rawTailLine(t, s, "sess-parented")
		val, ok := raw["parent_agent_id"]
		if !ok {
			t.Fatal("raw JSONL is missing the parent_agent_id key")
		}
		if string(val) != `"mia"` {
			t.Errorf("raw parent_agent_id = %s, want \"mia\"", val)
		}
	})

	t.Run("empty parent still writes a present key (no omitempty)", func(t *testing.T) {
		s := newTestLifecycleStore(t)
		// The store itself does not reject an empty parent — the guard lives
		// at the delegate mint site (FR-015). What the store MUST do is make
		// the emptiness visible on disk rather than eliding the key.
		if err := s.Persist(&LifecycleRecord{
			SessionID: "sess-orphan", State: LifecycleQueued,
			OwnerScopeKind: OwnerScopeHuman,
			ParentAgentID:  "", ParentDurableKey: "transcript-1",
			WorkspaceID: "ws-1", AgentID: "ray",
		}); err != nil {
			t.Fatalf("persist: %v", err)
		}

		raw := rawTailLine(t, s, "sess-orphan")
		val, ok := raw["parent_agent_id"]
		if !ok {
			t.Fatal("raw JSONL elided the parent_agent_id key for an empty value — " +
				"the field must NOT carry `omitempty` (FR-015): an absent key is " +
				"indistinguishable from a pre-field record and cannot be audited")
		}
		if string(val) != `""` {
			t.Errorf("raw parent_agent_id = %s, want an empty string", val)
		}
	})
}

// seedParentTestRecords persists a deliberately adversarial fixture set: the
// three sessions SHARE a ParentDurableKey (which is what a real parent and
// its children do — pkg/agent/subturn.go reuses the transcript session id),
// and one of them is run BY the agent under test rather than started by it.
func seedParentTestRecords(t *testing.T, s *LifecycleStore) {
	t.Helper()
	recs := []*LifecycleRecord{
		// Started by mia. The only record a "subagents mia started" query
		// may return.
		{
			SessionID: "mine", State: LifecycleRunning,
			OwnerScopeKind: OwnerScopeHuman,
			ParentAgentID:  "mia", ParentDurableKey: "shared-transcript",
			WorkspaceID: "ws-1", AgentID: "ray",
		},
		// A SIBLING: started by jim, but sharing mia's ParentDurableKey and
		// carrying the same empty OwnerScopeID a top-level delegation has.
		// Inferring parentage from either field would wrongly return this.
		{
			SessionID: "sibling", State: LifecycleRunning,
			OwnerScopeKind: OwnerScopeHuman,
			ParentAgentID:  "jim", ParentDurableKey: "shared-transcript",
			WorkspaceID: "ws-1", AgentID: "ava",
		},
		// A session mia RUNS (she is the child/target) but did not start.
		// Inferring parentage from AgentID would wrongly return this.
		{
			SessionID: "mia-is-the-child", State: LifecycleRunning,
			OwnerScopeKind: OwnerScopeHuman,
			ParentAgentID:  "jim", ParentDurableKey: "shared-transcript",
			WorkspaceID: "ws-1", AgentID: "mia",
		},
	}
	for _, r := range recs {
		if err := s.Persist(r); err != nil {
			t.Fatalf("seed %q: %v", r.SessionID, err)
		}
	}
}

func listedSessionIDs(recs []LifecycleRecord) []string {
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, r.SessionID)
	}
	return out
}

// TestLifecycleFilter_ParentAgentIDMatches proves the filter answers "the
// subagents I started" exactly — and that a parent is never inferred from a
// shared field (ParentDurableKey) or a child-owned one (AgentID).
func TestLifecycleFilter_ParentAgentIDMatches(t *testing.T) {
	s := newTestLifecycleStore(t)
	seedParentTestRecords(t, s)

	got, err := s.List(LifecycleFilter{ParentAgentID: "mia"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids := listedSessionIDs(got)
	if len(ids) != 1 || ids[0] != "mine" {
		t.Fatalf("List(ParentAgentID=mia) = %v, want exactly [mine] — "+
			"a sibling sharing ParentDurableKey or a session mia merely RUNS leaked in", ids)
	}

	// The reciprocal query must be equally exact.
	got, err = s.List(LifecycleFilter{ParentAgentID: "jim"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids = listedSessionIDs(got)
	if len(ids) != 2 {
		t.Fatalf("List(ParentAgentID=jim) = %v, want the 2 records jim started", ids)
	}

	// AgentID is a genuinely different axis: it matches the CHILD.
	got, err = s.List(LifecycleFilter{AgentID: "mia"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	ids = listedSessionIDs(got)
	if len(ids) != 1 || ids[0] != "mia-is-the-child" {
		t.Fatalf("List(AgentID=mia) = %v, want exactly [mia-is-the-child]", ids)
	}

	// An agent that started nothing gets nothing — never a fallback to "all".
	got, err = s.List(LifecycleFilter{ParentAgentID: "nobody"})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("List(ParentAgentID=nobody) = %v, want no rows", listedSessionIDs(got))
	}
}

// TestLifecycleFilter_UnsetParentAgentIDUnchangedBehaviour proves an UNSET
// filter field still means "filter off" — the ADR-053 boot sweep's own
// queries leave ParentAgentID unset and must keep seeing every record,
// including one whose own ParentAgentID is empty.
func TestLifecycleFilter_UnsetParentAgentIDUnchangedBehaviour(t *testing.T) {
	s := newTestLifecycleStore(t)
	seedParentTestRecords(t, s)
	if err := s.Persist(&LifecycleRecord{
		SessionID: "no-parent", State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman,
		ParentAgentID:  "", ParentDurableKey: "shared-transcript",
		WorkspaceID: "ws-1", AgentID: "ray",
	}); err != nil {
		t.Fatalf("seed no-parent: %v", err)
	}

	got, err := s.List(LifecycleFilter{})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("List(zero filter) returned %d records (%v), want all 4 — "+
			"an unset ParentAgentID must not filter anything", len(got), listedSessionIDs(got))
	}

	// Still true when the sweep's real query shape is used.
	got, err = s.List(LifecycleFilter{NonTerminalOnly: true})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("List(NonTerminalOnly) returned %d records (%v), want all 4", len(got), listedSessionIDs(got))
	}
}
