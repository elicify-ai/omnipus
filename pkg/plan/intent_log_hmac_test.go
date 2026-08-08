// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// intent_log_hmac_test.go covers the fix-wave finding 5 hardening of
// resolveChainKey (14-reviewer sign-off): a production binary that somehow
// reaches NewIntentLog/resolveChainKey with no configured chain key must
// fail closed with an error, never silently sign every intent-log record
// with the public, hardcoded dev-only constant. See intent_log_hmac.go's
// intentLogIsTestingFn doc comment for why the seam exists — testing.Testing()
// is unconditionally true inside every go test binary, so the production
// branch is otherwise unreachable from any test.
package plan

import (
	"path/filepath"
	"testing"
)

// withIntentLogNotTesting temporarily makes resolveChainKey believe it is
// running in a production (non-`go test`) binary, restoring the real
// testing.Testing()-backed behavior on cleanup.
func withIntentLogNotTesting(t *testing.T) {
	t.Helper()
	prev := intentLogIsTestingFn
	intentLogIsTestingFn = func() bool { return false }
	t.Cleanup(func() { intentLogIsTestingFn = prev })
}

// TestResolveChainKey_NonEmptyKeyAlwaysWins proves the caller-supplied key
// path is unaffected by the fix — it never consults intentLogIsTestingFn at
// all, in a real production binary or under test.
func TestResolveChainKey_NonEmptyKeyAlwaysWins(t *testing.T) {
	key := []byte("a-real-derived-32-byte-subkey!!!")
	got, err := resolveChainKey(key)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(got) != string(key) {
		t.Fatalf("got %q, want %q", got, key)
	}

	withIntentLogNotTesting(t)
	got2, err2 := resolveChainKey(key)
	if err2 != nil {
		t.Fatalf("unexpected error with a real key in a simulated production binary: %v", err2)
	}
	if string(got2) != string(key) {
		t.Fatalf("got %q, want %q", got2, key)
	}
}

// TestResolveChainKey_DevFallback_OnlyUnderTest proves the dev-only fallback
// key still works for tests/early-dev `go test` runs with no configured key
// (intentLogIsTestingFn true, the real default).
func TestResolveChainKey_DevFallback_OnlyUnderTest(t *testing.T) {
	got, err := resolveChainKey(nil)
	if err != nil {
		t.Fatalf("expected the dev-only fallback under go test, got error: %v", err)
	}
	if len(got) == 0 {
		t.Fatal("expected a non-empty deterministic dev-only key")
	}
}

// TestResolveChainKey_ProductionFailsClosedOnEmptyKey is the fix-wave
// finding 5 regression: outside a `go test` binary, an empty/nil key must
// return an error, NEVER the public dev-only constant.
func TestResolveChainKey_ProductionFailsClosedOnEmptyKey(t *testing.T) {
	withIntentLogNotTesting(t)

	got, err := resolveChainKey(nil)
	if err == nil {
		t.Fatalf("expected an error when no chain key is configured outside a test binary "+
			"(production fail-closed) — got a key instead (dev-only fallback leaked into production): %x", got)
	}
	if len(got) != 0 {
		t.Errorf("expected a nil/empty key alongside the error, got %d bytes", len(got))
	}
}

// TestNewIntentLog_FailsClosedOnEmptyKeyInProduction proves NewIntentLog
// itself propagates resolveChainKey's production error rather than
// constructing an IntentLog with the dev-only key.
func TestNewIntentLog_FailsClosedOnEmptyKeyInProduction(t *testing.T) {
	withIntentLogNotTesting(t)

	dir := filepath.Join(t.TempDir(), "plan_intents")
	il, err := NewIntentLog(dir)
	if err == nil {
		t.Fatal("expected NewIntentLog to fail closed when no chain key is configured in production")
	}
	if il != nil {
		t.Error("expected a nil *IntentLog alongside the error")
	}
}

// TestNewIntentLog_RealKeyStillWorksInProduction is the control: a
// real caller-supplied key still constructs successfully even when
// intentLogIsTestingFn reports false (production).
func TestNewIntentLog_RealKeyStillWorksInProduction(t *testing.T) {
	withIntentLogNotTesting(t)

	dir := filepath.Join(t.TempDir(), "plan_intents")
	key := []byte("a-real-derived-32-byte-subkey!!!")
	il, err := NewIntentLog(dir, key)
	if err != nil {
		t.Fatalf("unexpected error with a real key: %v", err)
	}
	if il == nil {
		t.Fatal("expected a non-nil *IntentLog")
	}
}
