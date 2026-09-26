// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-093 D1 — "The parent lock is for ordering. An unchanged parent is not
// rewritten": PublishChildUnderParentLock appends the parent line ONLY when
// the parent record did not exist (the mint case, existed == false). A launch
// whose parent already has a record must leave the parent's JSONL byte-for-
// byte unchanged — today the store appends an unchanged copy of the parent on
// every publication, which is the write the #890 error came from.
//
// Oracles are ADR-093 (docs/internal/architecture/ADR-093-open-conversation-
// must-keep-delegation.md) D1 and its test-plan row "Chat root running, no
// Stop. Delegate. → Child published. Parent file does not gain a line", not
// the current implementation.

package session

import (
	"os"
	"strings"
	"testing"
)

// adr093ParentLineCount counts the JSONL lines in a lifecycle record file.
// D1's observable is the parent FILE, not the loaded tail: "the parent JSONL
// does not grow by a duplicate snapshot" (ADR-093 Positive consequences).
func adr093ParentLineCount(t *testing.T, s *LifecycleStore, id string) int {
	t.Helper()
	raw, err := os.ReadFile(s.path(id))
	if err != nil {
		t.Fatalf("read parent file for %s: %v", id, err)
	}
	trimmed := strings.TrimRight(string(raw), "\n")
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}

// TestAdr093PublishChild_ExistingParentDoesNotGainALine pins D1's rule on the
// store primitive directly: an existing parent's file gains no line when a
// child is published under it. RED today — persistLocked(parentRec) runs on
// every publication, so the parent gains a duplicate line.
func TestAdr093PublishChild_ExistingParentDoesNotGainALine(t *testing.T) {
	s := newTestLifecycleStore(t)
	const parent = "adr093-parent-existing"
	const child = "adr093-child-existing"

	if err := s.Persist(&LifecycleRecord{
		SessionID:      parent,
		Generation:     1,
		State:          LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman,
		WorkspaceID:    "ws-adr093",
		AgentID:        "chat-agent",
		Origin:         &Origin{Kind: OriginKindChat},
	}); err != nil {
		t.Fatalf("persist parent: %v", err)
	}
	before := adr093ParentLineCount(t, s, parent)
	if before != 1 {
		t.Fatalf("parent file has %d lines before publication, want 1", before)
	}

	err := s.PublishChildUnderParentLock(parent, func(parentRec *LifecycleRecord, existed bool) (*LifecycleRecord, error) {
		if !existed {
			t.Fatal("existed = false, want true (the parent record was persisted above)")
		}
		// The callback only READS an existing parent (launchSteered's shape);
		// it must not have to change it for the parent file to stay put.
		return &LifecycleRecord{
			SessionID:      child,
			Generation:     1,
			State:          LifecycleQueued,
			OwnerScopeKind: OwnerScopeParentSession,
			OwnerScopeID:   parent,
			WorkspaceID:    "ws-adr093",
			AgentID:        "worker-agent",
			SteeredBy: &SteeredBy{
				SteeringSessionID: parent,
				RootSessionID:     parent,
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("PublishChildUnderParentLock: %v", err)
	}

	childRec, loadErr := s.Load(child)
	if loadErr != nil {
		t.Fatalf("Load(child) after publication: %v", loadErr)
	}
	if childRec.SessionID != child {
		t.Fatalf("published child record is for %q, want %q", childRec.SessionID, child)
	}

	after := adr093ParentLineCount(t, s, parent)
	if after != before {
		t.Fatalf("parent file grew from %d to %d lines — ADR-093 D1: an unchanged parent must not be rewritten on publication", before, after)
	}
}

// TestAdr093PublishChild_MintCaseStillAppendsParent pins the other half of
// D1: the mint case (existed == false — the steering session's first
// delegation) still appends the parent's own line, inside the lock. This is
// ADR-091 D1's first-delegation write, unchanged by ADR-093.
func TestAdr093PublishChild_MintCaseStillAppendsParent(t *testing.T) {
	s := newTestLifecycleStore(t)
	const parent = "adr093-parent-mint"
	const child = "adr093-child-mint"

	err := s.PublishChildUnderParentLock(parent, func(parentRec *LifecycleRecord, existed bool) (*LifecycleRecord, error) {
		if existed {
			t.Fatal("existed = true, want false (nothing was persisted for the parent yet)")
		}
		parentRec.Generation = 1
		parentRec.State = LifecycleRunning
		parentRec.OwnerScopeKind = OwnerScopeHuman
		parentRec.WorkspaceID = "ws-adr093"
		parentRec.AgentID = "chat-agent"
		parentRec.Origin = &Origin{Kind: OriginKindChat}
		return &LifecycleRecord{
			SessionID:      child,
			Generation:     1,
			State:          LifecycleQueued,
			OwnerScopeKind: OwnerScopeParentSession,
			OwnerScopeID:   parent,
			WorkspaceID:    "ws-adr093",
			AgentID:        "worker-agent",
			SteeredBy: &SteeredBy{
				SteeringSessionID: parent,
				RootSessionID:     parent,
			},
		}, nil
	})
	if err != nil {
		t.Fatalf("PublishChildUnderParentLock (mint): %v", err)
	}

	parentRec, loadErr := s.Load(parent)
	if loadErr != nil {
		t.Fatalf("Load(parent) after mint publication: %v", loadErr)
	}
	if parentRec.Generation != 1 || parentRec.State != LifecycleRunning {
		t.Fatalf("minted parent = gen %d state %q, want gen 1 running", parentRec.Generation, parentRec.State)
	}
	if lines := adr093ParentLineCount(t, s, parent); lines != 1 {
		t.Fatalf("minted parent file has %d lines, want exactly 1 (the mint write)", lines)
	}
	childRec, loadErr := s.Load(child)
	if loadErr != nil {
		t.Fatalf("Load(child) after mint publication: %v", loadErr)
	}
	if childRec.SessionID != child {
		t.Fatalf("published child record is for %q, want %q", childRec.SessionID, child)
	}
}
