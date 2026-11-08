// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Pins and the boundary probe for the ownership walk's depth bound when
// SetOwnershipWalkMaxDepth is NEVER called (the unset fallback path). The
// explicit-bound path is covered by the ADR-091 authority tests.

package tools

import (
	"context"
	"fmt"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/session"
)

// TestDefaultOwnershipWalkMaxDepthPin_IsTen pins this package's mirror of the
// shipped delegation-depth backstop to the literal 10 (founder ruling
// 2026-10-27). pkg/tools cannot import pkg/agent's unexported constant, so
// the mirror is pinned here, in the package that owns it.
func TestDefaultOwnershipWalkMaxDepthPin_IsTen(t *testing.T) {
	if defaultOwnershipWalkMaxDepth != 10 {
		t.Fatalf("defaultOwnershipWalkMaxDepth = %d, want the literal 10 (founder ruling 2026-10-27)", defaultOwnershipWalkMaxDepth)
	}
}

// TestOwnershipWalk_UnsetBound_ReachesTenHopsNotEleven probes the boundary of
// the unset fallback: with the walk bound never set, an ancestor exactly 10
// hops up is reached (authorized) and one 11 hops up is not (denied) — the
// bound must be the shipped default 10, not the old 3 and not any other
// value. Literal distances on purpose.
func TestOwnershipWalk_UnsetBound_ReachesTenHopsNotEleven(t *testing.T) {
	store := session.NewLifecycleStore(t.TempDir())
	tool := NewDelegateTool("test", 0, 0)
	tool.SetLifecycleStore(store)
	// Deliberately NO SetOwnershipWalkMaxDepth call — the unset fallback under
	// test must be defaultOwnershipWalkMaxDepth.
	if got := tool.ownershipMaxDepth(); got != 10 {
		t.Fatalf("ownershipMaxDepth with no explicit bound = %d, want the literal 10", got)
	}

	const caller = "walk-depth-root-caller"
	// Chain: c1 steered by the caller, c2 steered by c1, … c11 by c10 — so the
	// caller sits exactly 10 hops above c10 and exactly 11 hops above c11.
	for i := 1; i <= 11; i++ {
		parent := caller
		if i > 1 {
			parent = fmt.Sprintf("walk-depth-c%d", i-1)
		}
		if err := store.Persist(adr091SteeredRecord(fmt.Sprintf("walk-depth-c%d", i), parent)); err != nil {
			t.Fatal(err)
		}
	}
	ctx := WithTranscriptSessionID(context.Background(), caller)

	// Exactly 10 hops: inside the default bound — must be authorized.
	if _, err := tool.verifyCallerPrincipal(ctx, adr091SteeredRecord("walk-depth-c10", "walk-depth-c9")); err != nil {
		t.Fatalf("caller exactly 10 hops up must be reached with the unset bound (default 10): %v", err)
	}
	// Exactly 11 hops: one past the default bound — must be denied.
	if _, err := tool.verifyCallerPrincipal(ctx, adr091SteeredRecord("walk-depth-c11", "walk-depth-c10")); err == nil {
		t.Fatal("caller exactly 11 hops up must be denied with the unset bound (default 10)")
	}
}
