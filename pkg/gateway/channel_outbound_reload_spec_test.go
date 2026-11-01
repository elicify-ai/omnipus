package gateway

import "testing"

// T-29/S-24: this must become the real REST + Manager.Reload interleaving,
// not a direct call to the pure policy predicate or a simulated reload.
// Required barriers: M2 paused immediately before the dequeue check, and the
// actual reload observed holding the manager write lock. Revoke through REST,
// release M2, and require both operations finish within 5s with no M2 Send.
// A GetChannel/LastStartError mutation in dequeue must fail on that bound.
func TestT29_ReloadAndDequeue_RealWriteLockInterleavingCannotDeadlockOrSendRevokedM2(t *testing.T) {
	t.Fatal("BLOCKED: deterministic pre-dequeue and real Manager.Reload write-lock-acquired observation seams not implemented on WP-2 — required by T-29/S-24/FR-044; limiter cancellation, a read-lock probe, or a fake reload cannot establish this interleaving")
}
