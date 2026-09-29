// Omnipus — RED test for spec "Library views, anywhere"
// (docs/internal/specs/library-views-anywhere-spec.md), FR-VA-038, TDD Plan
// row 87 (va-qa3 dispatch, P4, team-lead ruling 2026-09-29).
//
// Oracle, verbatim: "The membership record is guarded by a dedicated
// exact-key membership lock (not the striped note-write locks, which were
// proven to self-deadlock on a collision). Test 87 (regression: two keys
// that collide in the striped table do not deadlock)."
//
// Verified by reading (2026-09-29): `grep -rn
// "stripedLock\|membershipLock\|MembershipLock" --include='*.go' .` finds
// exactly ONE stripedLock type, `pkg/entity/lock.go::stripedLock` — a
// fixed-size sharded mutex pool for `Store[T]`'s generic file-locking
// primitive, entirely unrelated to view membership or `.base` files (its
// own doc comment: "the single, process-wide stripedLock shared by every
// Store[T]"). No `membershipLock`/`MembershipLock` symbol exists anywhere.
//
// A deterministic barrier proving the NEW dedicated lock does not deadlock
// on two colliding keys needs the lock itself to exist first — there is
// nothing to construct the regression fixture against (per FR-VA-038, the
// OLD striped table is what self-deadlocks; the requirement is that the
// NEW lock does not repeat that failure, which cannot be exercised before
// the new lock exists).
//
// BLOCKED per the qa-lead RED protocol: the dedicated exact-key membership
// lock this test must exercise does not exist anywhere in the codebase.
//
// Run (one at a time, per omnipus-shared-rules rule 2):
//
//	CGO_ENABLED=0 go test -tags goolm,stdjson -count=1 -p 1 -run '^TestMembershipLock_TwoCollidingKeysDoNotDeadlock$' ./pkg/gateway/
//
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import "testing"

func TestMembershipLock_TwoCollidingKeysDoNotDeadlock(t *testing.T) {
	t.Fatal("BLOCKED: FR-VA-038/test 87 needs the DEDICATED exact-key membership lock itself to exist " +
		"before a deterministic two-colliding-keys barrier could exercise it — no membershipLock/ " +
		"MembershipLock symbol exists anywhere in the codebase (confirmed by grep; the only " +
		"stripedLock, pkg/entity/lock.go, is Store[T]'s generic file-locking primitive, unrelated to " +
		"view membership). There is nothing to construct the regression fixture against.")
}
