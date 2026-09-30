// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package task

import (
	"os"
	"testing"
)

// AtomicWriteFuncForTest is the existing store filesystem-write seam's signature.
// It is compiled only into this package's test binary, never production.
type AtomicWriteFuncForTest = func(string, []byte, os.FileMode) error

// InterceptAtomicWritesForTest exposes the existing writeFileAtomicFn seam to
// external task_test integration tests. The interceptor receives the original
// atomic writer so every unaffected file can still be written for real.
//
// Tests using this global seam must be serial and register worker/loop shutdown
// AFTER installing it: cleanup drains every writer before restoring the seam.
func InterceptAtomicWritesForTest(
	t testing.TB,
	intercept func(string, []byte, os.FileMode, AtomicWriteFuncForTest) error,
) {
	t.Helper()
	original := writeFileAtomicFn
	writeFileAtomicFn = func(path string, data []byte, perm os.FileMode) error {
		return intercept(path, data, perm, original)
	}
	t.Cleanup(func() { writeFileAtomicFn = original })
}
