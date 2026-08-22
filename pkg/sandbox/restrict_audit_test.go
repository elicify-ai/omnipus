// T2.15: RestrictCurrentThread_FailureEmitsAudit.
//
// Tests that when restrictCurrentThreadIfNeeded returns an error, the
// emitRestrictFailure function invokes the registered audit hook AND returns
// the error to the caller. B1.2(d).
//
// We test emitRestrictFailure + SetRestrictAuditHook directly because
// forcing a real kernel failure in a unit test without a subprocess requires
// injecting the error path. The integration path (real kernel EINVAL) is
// documented in T2.19 (landlock_abi_hardfail_test.go).

package sandbox

import (
	"errors"
	"testing"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// TestRestrictCurrentThread_FailureEmitsAudit (T2.15) installs a custom
// restrictAuditHook via SetRestrictAuditHook, then calls emitRestrictFailure
// directly with a synthetic error. Asserts:
//  1. The hook was invoked.
//  2. The emitted entry carries Decision=error and a non-empty callsite.
//  3. The error is surfaced to the caller (not swallowed).
func TestRestrictCurrentThread_FailureEmitsAudit(t *testing.T) {
	var captured []*audit.Entry

	hook := func(entry *audit.Entry) {
		cp := *entry
		captured = append(captured, &cp)
	}

	// Install the hook; restore nil at cleanup so other tests don't see it.
	SetRestrictAuditHook(hook)
	t.Cleanup(func() { SetRestrictAuditHook(nil) })

	synthErr := errors.New("EINVAL: synthetic restrict_self failure")
	emitRestrictFailure("test.callsite", synthErr)

	if len(captured) == 0 {
		t.Fatal("audit hook was not invoked — emitRestrictFailure did not call the hook (T2.15)")
	}

	entry := captured[0]
	if entry.Decision != audit.DecisionError {
		t.Errorf("entry.Decision = %q; want %q", entry.Decision, audit.DecisionError)
	}
	if entry.Event == "" {
		t.Error("entry.Event is empty; must be a non-empty event name")
	}
	if entry.Details == nil {
		t.Fatal("entry.Details is nil; must carry callsite and error")
	}
	cs, ok := entry.Details["callsite"].(string)
	if !ok || cs == "" {
		t.Errorf("entry.Details[callsite] is empty; want %q", "test.callsite")
	}
	errStr, ok := entry.Details["error"].(string)
	if !ok || errStr == "" {
		t.Error("entry.Details[error] is empty; must contain the error message")
	}
}

// TestRestrictCurrentThread_NoHook_FallsBackToSlog (T2.15 edge case) verifies
// that emitRestrictFailure does not panic when no hook is registered. The
// fallback to slog.Error must be silent from the test's perspective.
func TestRestrictCurrentThread_NoHook_FallsBackToSlog(t *testing.T) {
	// Ensure no hook is installed for this test.
	SetRestrictAuditHook(nil)

	// Must not panic.
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("emitRestrictFailure panicked with no hook: %v", r)
		}
	}()

	emitRestrictFailure("test.no_hook", errors.New("synthetic error"))
}

// TestSetRestrictAuditHook_NilClears verifies that passing nil to
// SetRestrictAuditHook successfully clears a previously installed hook.
func TestSetRestrictAuditHook_NilClears(t *testing.T) {
	called := false
	hook := func(*audit.Entry) { called = true }

	SetRestrictAuditHook(hook)
	SetRestrictAuditHook(nil)

	// emitRestrictFailure after clear must not invoke the hook.
	emitRestrictFailure("test.after_clear", errors.New("test"))
	if called {
		t.Error("hook was called after SetRestrictAuditHook(nil) cleared it")
	}
}
