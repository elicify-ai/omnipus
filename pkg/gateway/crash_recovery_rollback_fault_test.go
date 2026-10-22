//go:build goolm && stdjson

package gateway

import "testing"

func TestCrashRecovery_RetryRollbackStorageFailureNeedsBoundary(t *testing.T) {
	// This is deliberately a loud pending implementation/testability finding,
	// not a sleep/race against task files and not a fake recording launcher.
	// The task package's atomic writer observer is package-private; the real
	// gateway Retry path exposes no post-transition/pre-rollback write barrier.
	t.Fatal("BLOCKED: deterministic post-task-transition/pre-rollback filesystem fault boundary is not exposed through real Board Retry — required by crash-recovery DESIGN Decision 4 / RED A visible rollback failure")
}
