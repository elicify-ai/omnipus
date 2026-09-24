// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// ADR-091 landing order I-1 — PublishChildUnderParentLock: "the launcher
// reads the parent's record (for depth, authorization and a
// current-generation Stop marker) and publishes the child's record under
// the parent's record lock, so a cascade cannot enumerate the parent's
// children between the check and the publication." lifecycleStripedLock
// (lifecycle_lock.go) is a 64-shard HASH-based pool, so two distinct
// session ids can map to the SAME *sync.Mutex — a naive nested Lock for
// the child (while the parent's lock is already held) would then
// self-deadlock (sync.Mutex is not reentrant). Every test here proves the
// primitive is correct for BOTH the common (different-shard) case and the
// same-shard collision case.

package session

import (
	"fmt"
	"sync"
	"testing"
	"time"
)

// findShardCollision returns two distinct ids that hash to the SAME
// lifecycleStripedLock shard as base — needed to exercise
// PublishChildUnderParentLock's same-shard branch deterministically rather
// than hoping for a 1/64 chance for two the test picks.
func findShardCollision(t *testing.T, s *LifecycleStore, base string) string {
	t.Helper()
	target := s.Lock(base)
	for i := 0; i < 100000; i++ {
		candidate := fmt.Sprintf("collision-candidate-%d", i)
		if candidate == base {
			continue
		}
		if s.Lock(candidate) == target {
			return candidate
		}
	}
	t.Fatal("findShardCollision: no colliding id found in 100000 attempts — the shard count or hash must have changed")
	return ""
}

func TestPublishChildUnderParentLock_MintsParentWhenMissing(t *testing.T) {
	s := newTestLifecycleStore(t)
	const parent = "parent-mint-1"
	const child = "child-mint-1"

	err := s.PublishChildUnderParentLock(parent, func(parentRec *LifecycleRecord, existed bool) (*LifecycleRecord, error) {
		if existed {
			t.Fatal("existed = true, want false (no record was persisted yet)")
		}
		parentRec.State = LifecycleRunning
		parentRec.OwnerScopeKind = OwnerScopeHuman
		parentRec.WorkspaceID = "ws-1"
		parentRec.AgentID = "chat-agent"
		parentRec.Generation = 1
		return &LifecycleRecord{
			SessionID: child, Generation: 1, State: LifecycleQueued,
			OwnerScopeKind: OwnerScopeParentSession, OwnerScopeID: parent,
			WorkspaceID: "ws-1", AgentID: "worker",
		}, nil
	})
	if err != nil {
		t.Fatalf("PublishChildUnderParentLock: %v", err)
	}

	parentRec, err := s.Load(parent)
	if err != nil {
		t.Fatalf("Load(parent): %v", err)
	}
	if parentRec.State != LifecycleRunning || parentRec.WorkspaceID != "ws-1" {
		t.Errorf("parent record = %+v, want the minted fields", parentRec)
	}

	childRec, err := s.Load(child)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if childRec.OwnerScopeID != parent {
		t.Errorf("child.OwnerScopeID = %q, want %q", childRec.OwnerScopeID, parent)
	}
}

func TestPublishChildUnderParentLock_UsesExistingParent(t *testing.T) {
	s := newTestLifecycleStore(t)
	const parent = "parent-existing-1"
	const child = "child-existing-1"

	if err := s.Persist(&LifecycleRecord{
		SessionID: parent, Generation: 3, State: LifecycleRunning,
		OwnerScopeKind: OwnerScopeHuman, WorkspaceID: "ws-orig", AgentID: "chat-agent",
	}); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	var sawGeneration int
	err := s.PublishChildUnderParentLock(parent, func(parentRec *LifecycleRecord, existed bool) (*LifecycleRecord, error) {
		if !existed {
			t.Fatal("existed = false, want true (a record was already persisted)")
		}
		sawGeneration = parentRec.Generation
		return &LifecycleRecord{
			SessionID: child, Generation: 1, State: LifecycleQueued,
			OwnerScopeKind: OwnerScopeParentSession, OwnerScopeID: parent,
			WorkspaceID: parentRec.WorkspaceID, AgentID: "worker",
		}, nil
	})
	if err != nil {
		t.Fatalf("PublishChildUnderParentLock: %v", err)
	}
	if sawGeneration != 3 {
		t.Errorf("fn observed parent Generation = %d, want 3 (the already-persisted value)", sawGeneration)
	}
	childRec, err := s.Load(child)
	if err != nil {
		t.Fatalf("Load(child): %v", err)
	}
	if childRec.WorkspaceID != "ws-orig" {
		t.Errorf("child.WorkspaceID = %q, want inherited ws-orig", childRec.WorkspaceID)
	}
}

func TestPublishChildUnderParentLock_NilChild_ParentOnlyWrite(t *testing.T) {
	s := newTestLifecycleStore(t)
	const parent = "parent-noop-child-1"

	err := s.PublishChildUnderParentLock(parent, func(parentRec *LifecycleRecord, existed bool) (*LifecycleRecord, error) {
		parentRec.State = LifecycleRunning
		parentRec.OwnerScopeKind = OwnerScopeHuman
		parentRec.WorkspaceID = "ws-1"
		parentRec.AgentID = "chat-agent"
		parentRec.Generation = 1
		return nil, nil // no child to publish this call
	})
	if err != nil {
		t.Fatalf("PublishChildUnderParentLock: %v", err)
	}
	if _, err := s.Load(parent); err != nil {
		t.Fatalf("Load(parent): %v, want the parent-only write to have landed", err)
	}
}

func TestPublishChildUnderParentLock_FnErrorAbortsBothWrites(t *testing.T) {
	s := newTestLifecycleStore(t)
	const parent = "parent-abort-1"
	const child = "child-abort-1"
	wantErr := fmt.Errorf("refused")

	err := s.PublishChildUnderParentLock(parent, func(parentRec *LifecycleRecord, existed bool) (*LifecycleRecord, error) {
		parentRec.State = LifecycleRunning
		parentRec.OwnerScopeKind = OwnerScopeHuman
		return nil, wantErr
	})
	if err != wantErr {
		t.Fatalf("PublishChildUnderParentLock error = %v, want %v", err, wantErr)
	}
	if _, err := s.Load(parent); err == nil {
		t.Fatal("Load(parent) succeeded, want ErrLifecycleNotFound — fn's error must abort the parent write too")
	}
	if _, err := s.Load(child); err == nil {
		t.Fatal("Load(child) succeeded, want ErrLifecycleNotFound")
	}
}

// TestPublishChildUnderParentLock_SameShardPair is the test the whole
// primitive exists to make safe: a parent/child pair that the striped lock
// hashes to the SAME shard. A naive "Lock(parent); ...; Lock(child)" would
// self-deadlock here (sync.Mutex is not reentrant); this must complete.
func TestPublishChildUnderParentLock_SameShardPair(t *testing.T) {
	s := newTestLifecycleStore(t)
	const parent = "parent-collision-base"
	child := findShardCollision(t, s, parent)
	if s.Lock(parent) != s.Lock(child) {
		t.Fatalf("test fixture bug: %q and %q do not actually collide", parent, child)
	}

	done := make(chan error, 1)
	go func() {
		done <- s.PublishChildUnderParentLock(parent, func(parentRec *LifecycleRecord, existed bool) (*LifecycleRecord, error) {
			parentRec.State = LifecycleRunning
			parentRec.OwnerScopeKind = OwnerScopeHuman
			parentRec.WorkspaceID = "ws-1"
			parentRec.AgentID = "chat-agent"
			parentRec.Generation = 1
			return &LifecycleRecord{
				SessionID: child, Generation: 1, State: LifecycleQueued,
				OwnerScopeKind: OwnerScopeParentSession, OwnerScopeID: parent,
				WorkspaceID: "ws-1", AgentID: "worker",
			}, nil
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("PublishChildUnderParentLock (same-shard pair): %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("PublishChildUnderParentLock deadlocked on a same-shard parent/child pair")
	}

	if _, err := s.Load(child); err != nil {
		t.Fatalf("Load(child) after same-shard publish: %v", err)
	}
}

// TestPublishChildUnderParentLock_ConcurrentDistinctParents_NeverDeadlocks
// is the -race concurrency proof: many goroutines publish children under
// MANY DIFFERENT parents (some of which the striped lock is statistically
// certain to collide on, given only 64 shards and hundreds of ids)
// simultaneously. Run with -race; the property under test is "completes
// promptly, every child lands, no lost or corrupted write" — never a
// deadlock and never a torn/partial record.
func TestPublishChildUnderParentLock_ConcurrentDistinctParents_NeverDeadlocks(t *testing.T) {
	s := newTestLifecycleStore(t)
	const n = 120
	var wg sync.WaitGroup
	errs := make(chan error, n)
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func(i int) {
			defer wg.Done()
			parent := fmt.Sprintf("conc-parent-%d", i%30) // 30 parents across n launches: guarantees repeat writers per parent AND shard collisions across the 64-shard pool
			child := fmt.Sprintf("conc-child-%d", i)
			err := s.PublishChildUnderParentLock(parent, func(parentRec *LifecycleRecord, existed bool) (*LifecycleRecord, error) {
				parentRec.State = LifecycleRunning
				parentRec.OwnerScopeKind = OwnerScopeHuman
				parentRec.Generation = 1
				if parentRec.WorkspaceID == "" {
					parentRec.WorkspaceID = "ws-1"
				}
				if parentRec.AgentID == "" {
					parentRec.AgentID = "chat-agent"
				}
				return &LifecycleRecord{
					SessionID: child, Generation: 1, State: LifecycleQueued,
					OwnerScopeKind: OwnerScopeParentSession, OwnerScopeID: parent,
					WorkspaceID: "ws-1", AgentID: "worker",
				}, nil
			})
			errs <- err
		}(i)
	}

	waitDone := make(chan struct{})
	go func() { wg.Wait(); close(waitDone) }()
	select {
	case <-waitDone:
	case <-time.After(45 * time.Second):
		t.Fatal("PublishChildUnderParentLock: concurrent run did not complete within 45s — suspect a deadlock")
	}
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatalf("PublishChildUnderParentLock (concurrent): %v", err)
		}
	}

	for i := 0; i < n; i++ {
		child := fmt.Sprintf("conc-child-%d", i)
		if _, err := s.Load(child); err != nil {
			t.Fatalf("Load(%s) after concurrent publish: %v", child, err)
		}
	}
}
