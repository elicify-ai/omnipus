package agent

import (
	"strings"
	"testing"
)

// TestRecordToolOutcome verifies the circuit-breaker logic in turnState.recordToolOutcome.
// These are pure unit tests — no AgentLoop, no LLM, no file I/O.
func TestRecordToolOutcome(t *testing.T) {
	t.Run("three consecutive exec failures trigger guidance on the third", func(t *testing.T) {
		ts := &turnState{}

		// First failure — below limit, no injection.
		guidance, inject := ts.recordToolOutcome("exec", true)
		if inject {
			t.Fatalf("1st failure: expected inject=false, got true (guidance=%q)", guidance)
		}
		if guidance != "" {
			t.Fatalf("1st failure: expected empty guidance, got %q", guidance)
		}
		if ts.consecutiveToolFailures != 1 {
			t.Fatalf("1st failure: expected counter=1, got %d", ts.consecutiveToolFailures)
		}

		// Second failure — still below limit.
		guidance, inject = ts.recordToolOutcome("exec", true)
		if inject {
			t.Fatalf("2nd failure: expected inject=false, got true (guidance=%q)", guidance)
		}
		if ts.consecutiveToolFailures != 2 {
			t.Fatalf("2nd failure: expected counter=2, got %d", ts.consecutiveToolFailures)
		}

		// Third failure — exactly at the limit; must inject.
		guidance, inject = ts.recordToolOutcome("exec", true)
		if !inject {
			t.Fatalf("3rd failure: expected inject=true (limit=%d)", consecutiveShellFailureLimit)
		}
		if guidance == "" {
			t.Fatal("3rd failure: expected non-empty guidance")
		}
		if ts.consecutiveToolFailures != 3 {
			t.Fatalf("3rd failure: expected counter=3, got %d", ts.consecutiveToolFailures)
		}
	})

	t.Run("fourth failure above limit does NOT inject again", func(t *testing.T) {
		ts := &turnState{}

		// Drive to the limit silently.
		for i := 0; i < consecutiveShellFailureLimit; i++ {
			ts.recordToolOutcome("exec", true) //nolint:errcheck
		}

		// One more above the limit — must be silent.
		guidance, inject := ts.recordToolOutcome("exec", true)
		if inject {
			t.Fatalf("4th failure (above limit): expected inject=false, got true (guidance=%q)", guidance)
		}
		if guidance != "" {
			t.Fatalf("4th failure (above limit): expected empty guidance, got %q", guidance)
		}
	})

	t.Run("success between failures resets counter", func(t *testing.T) {
		ts := &turnState{}

		// Two failures.
		ts.recordToolOutcome("exec", true)
		ts.recordToolOutcome("exec", true)
		if ts.consecutiveToolFailures != 2 {
			t.Fatalf("after 2 failures: expected counter=2, got %d", ts.consecutiveToolFailures)
		}

		// A success resets.
		guidance, inject := ts.recordToolOutcome("exec", false)
		if inject {
			t.Fatalf("success: expected inject=false, got true (guidance=%q)", guidance)
		}
		if ts.consecutiveToolFailures != 0 {
			t.Fatalf("after success: expected counter=0, got %d", ts.consecutiveToolFailures)
		}

		// Now it takes a fresh 3 failures to fire again.
		ts.recordToolOutcome("exec", true)
		ts.recordToolOutcome("exec", true)
		_, inject = ts.recordToolOutcome("exec", true)
		if !inject {
			t.Fatal("after reset + 3 more failures: expected inject=true on the 3rd")
		}
	})

	t.Run("non-provisioning tool failure does not increment counter", func(t *testing.T) {
		ts := &turnState{}

		// Two exec failures first to set a baseline.
		ts.recordToolOutcome("exec", true)
		ts.recordToolOutcome("exec", true)

		// A read_file failure must NOT increment toward the limit.
		guidance, inject := ts.recordToolOutcome("read_file", true)
		if inject {
			t.Fatalf("read_file failure: expected inject=false, got true (guidance=%q)", guidance)
		}
		// Counter must have reset (non-provisioning failure resets, same as success).
		if ts.consecutiveToolFailures != 0 {
			t.Fatalf("after read_file failure: expected counter reset to 0, got %d", ts.consecutiveToolFailures)
		}
	})

	t.Run("workspace_shell counts toward the same limit as exec", func(t *testing.T) {
		ts := &turnState{}

		ts.recordToolOutcome("exec", true)
		ts.recordToolOutcome("workspace_shell", true)
		guidance, inject := ts.recordToolOutcome("workspace_shell", true)
		if !inject {
			t.Fatal("mixed exec+workspace_shell failures: expected inject=true on the 3rd")
		}
		if guidance == "" {
			t.Fatal("mixed: expected non-empty guidance")
		}
	})

	t.Run("workspace_shell_bg counts toward the same limit as exec", func(t *testing.T) {
		ts := &turnState{}

		// A model flailing via background shell (`apt install &`) must not
		// bypass the breaker. workspace_shell_bg must be in provisioningToolNames.
		ts.recordToolOutcome("exec", true)
		ts.recordToolOutcome("workspace_shell_bg", true)
		guidance, inject := ts.recordToolOutcome("workspace_shell_bg", true)
		if !inject {
			t.Fatal("mixed exec+workspace_shell_bg failures: expected inject=true on the 3rd")
		}
		if guidance == "" {
			t.Fatal("mixed workspace_shell_bg: expected non-empty guidance")
		}
	})

	t.Run("guidance contains fetch_url", func(t *testing.T) {
		ts := &turnState{}
		ts.recordToolOutcome("exec", true)
		ts.recordToolOutcome("exec", true)
		guidance, inject := ts.recordToolOutcome("exec", true)
		if !inject {
			t.Fatal("expected inject=true on 3rd failure")
		}
		if !strings.Contains(guidance, "fetch_url") {
			t.Fatalf("guidance must contain 'fetch_url'; got: %q", guidance)
		}
	})
}

// TestToolFailureBreakerConst verifies the limit constant value matches the spec.
func TestToolFailureBreakerConst(t *testing.T) {
	if consecutiveShellFailureLimit != 3 {
		t.Fatalf("consecutiveShellFailureLimit must be 3, got %d", consecutiveShellFailureLimit)
	}
}

// TestProvisioningToolSet verifies that provisioningToolNames is the single
// source of truth and contains exactly the three expected shell tools. This
// catches a future omission when a new shell tool is added to the binary.
func TestProvisioningToolSet(t *testing.T) {
	expected := []string{"exec", "workspace_shell", "workspace_shell_bg"}
	for _, name := range expected {
		if !isProvisioningTool(name) {
			t.Errorf("isProvisioningTool(%q) = false; want true — add it to provisioningToolNames", name)
		}
	}
	// A non-shell tool must not appear in the set.
	for _, name := range []string{"read_file", "fetch_url", "dangerous_tool", ""} {
		if isProvisioningTool(name) {
			t.Errorf("isProvisioningTool(%q) = true; want false — non-shell tools must not be tracked", name)
		}
	}
}
