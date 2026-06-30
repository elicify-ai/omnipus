package tools

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"
)

// TestSummarizeSandboxDenial is a table-driven unit test for the
// summarizeSandboxDenial helper. It verifies:
//   - known denial signatures → blocked=true
//   - normal failure output    → blocked=false
//   - the returned summary never leaks raw kernel strings
func TestSummarizeSandboxDenial(t *testing.T) {
	t.Parallel()

	type tc struct {
		name        string
		stderr      string
		wantBlocked bool
		// bannedInSummary lists raw kernel strings that must NOT appear in the
		// returned summary when blocked=true.
		bannedInSummary []string
	}

	cases := []tc{
		{
			name:        "bash fork Cannot allocate memory",
			stderr:      "bash: fork: Cannot allocate memory",
			wantBlocked: true,
			bannedInSummary: []string{
				"Cannot fork",
				"Permission denied",
			},
		},
		{
			name:        "Cannot fork direct",
			stderr:      "Cannot fork",
			wantBlocked: true,
			bannedInSummary: []string{
				"Cannot fork",
				"Permission denied",
			},
		},
		{
			name:        "fork retry",
			stderr:      "fork: retry: Resource temporarily unavailable",
			wantBlocked: true,
			bannedInSummary: []string{
				"Permission denied",
			},
		},
		{
			name:        "apt lock file permission denied",
			stderr:      "E: Could not open lock file /var/lib/apt/lists/lock - Permission denied",
			wantBlocked: true,
			bannedInSummary: []string{
				"Permission denied",
				"/var/lib/apt",
			},
		},
		{
			name:        "dpkg lock permission denied",
			stderr:      "E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 123",
			wantBlocked: true,
			bannedInSummary: []string{
				"Permission denied",
			},
		},
		// FIX A1: "operation not permitted" from mount has no spawn context →
		// NOT a sandbox denial. The real sandbox path includes a token like
		// "fork", "exec", "seccomp", etc.
		{
			name:        "mount operation not permitted — no spawn context",
			stderr:      "mount: operation not permitted",
			wantBlocked: false,
		},
		// FIX A1: EPERM with a spawn context token (seccomp) is still blocked.
		{
			name:        "seccomp operation not permitted — spawn context present",
			stderr:      "seccomp: operation not permitted",
			wantBlocked: true,
			bannedInSummary: []string{
				"Permission denied",
			},
		},
		{
			name:        "chromium snap required",
			stderr:      "Command '/usr/bin/chromium-browser' requires the chromium snap to be installed.\nPlease install it with:\n\nsnap install chromium\n",
			wantBlocked: true,
			bannedInSummary: []string{
				"Permission denied",
				"Cannot fork",
			},
		},
		{
			name:        "snap install line",
			stderr:      "error: snap install failed: access denied",
			wantBlocked: true,
			bannedInSummary: []string{
				"Permission denied",
			},
		},
		// FIX A1 — false-positive cases: ordinary EPERM/EAGAIN must NOT be
		// classified as sandbox denials when there is no spawn/package-manager
		// context token co-occurring.
		{
			name: "chmod EPERM — no spawn context",
			// Typical output of: chmod 600 /some/read-only-file
			stderr:      "chmod: changing permissions of 'x': Operation not permitted",
			wantBlocked: false,
		},
		{
			name:        "kill EPERM — no spawn context",
			stderr:      "kill: (12345): Operation not permitted",
			wantBlocked: false,
		},
		{
			name: "benign EAGAIN — no spawn context (busy socket/fd)",
			// EAGAIN from a non-blocking read on a socket; no fork/exec context
			stderr:      "read: Resource temporarily unavailable",
			wantBlocked: false,
		},
		// FIX A1 — true-positive: EPERM WITH a spawn context token is still
		// classified as a sandbox denial.
		{
			name:        "fork EPERM — spawn context present, stays blocked",
			stderr:      "fork: Operation not permitted",
			wantBlocked: true,
			bannedInSummary: []string{
				"Permission denied",
				"Operation not permitted",
			},
		},
		// Normal failures must NOT be blocked.
		{
			name:        "no such file",
			stderr:      "ls: cannot access 'x': No such file or directory",
			wantBlocked: false,
		},
		{
			name:        "compile error",
			stderr:      "main.go:5:2: undefined: foo",
			wantBlocked: false,
		},
		{
			name:        "exit code only",
			stderr:      "",
			wantBlocked: false,
		},
		{
			name:        "permission denied on user file",
			stderr:      "cat: secret.txt: Permission denied",
			wantBlocked: false, // no apt/dpkg/snap/lock path → not a sandbox denial
		},
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			summary, blocked := summarizeSandboxDenial(c.stderr)

			if blocked != c.wantBlocked {
				t.Errorf("summarizeSandboxDenial(%q): blocked=%v, want %v (summary=%q)",
					c.stderr, blocked, c.wantBlocked, summary)
				return
			}

			if blocked {
				if summary == "" {
					t.Errorf("blocked=true but summary is empty")
				}
				for _, banned := range c.bannedInSummary {
					if strings.Contains(summary, banned) {
						t.Errorf("summary leaks raw kernel string %q: got %q", banned, summary)
					}
				}
			} else {
				if summary != "" {
					t.Errorf("blocked=false but summary is non-empty: %q", summary)
				}
			}
		})
	}
}

// TestSandboxDenialResult verifies the sandboxDenialResult helper's output contract:
//   - exit code is preserved in the message
//   - exit code -1 produces "killed by signal" wording
//   - stdout is appended when present
//   - ForUser is always empty (kernel text must not leak)
//   - Guidance is always set
//   - IsError is always true
func TestSandboxDenialResult(t *testing.T) {
	t.Parallel()

	t.Run("exit code preserved in message", func(t *testing.T) {
		t.Parallel()
		res := sandboxDenialResult(2, "")
		if !res.IsError {
			t.Errorf("expected IsError=true")
		}
		if res.ForUser != "" {
			t.Errorf("ForUser must be empty, got %q", res.ForUser)
		}
		if res.Guidance == "" {
			t.Errorf("expected Guidance to be set")
		}
		if !strings.Contains(res.ForLLM, "exited with code 2") {
			t.Errorf("expected exit code 2 in ForLLM, got %q", res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "killed by signal") {
			t.Errorf("unexpected 'killed by signal' for exitCode=2: %q", res.ForLLM)
		}
	})

	t.Run("exit code -1 produces killed by signal wording", func(t *testing.T) {
		t.Parallel()
		res := sandboxDenialResult(-1, "")
		if !strings.Contains(res.ForLLM, "killed by signal") {
			t.Errorf("expected 'killed by signal' for exitCode=-1, got %q", res.ForLLM)
		}
		if strings.Contains(res.ForLLM, "exited with code -1") {
			t.Errorf("unexpected numeric exit code in signal-kill message: %q", res.ForLLM)
		}
	})

	t.Run("stdout appended when present", func(t *testing.T) {
		t.Parallel()
		res := sandboxDenialResult(1, "some output before denial")
		if !strings.Contains(res.ForLLM, "some output before denial") {
			t.Errorf("expected stdout appended in ForLLM, got %q", res.ForLLM)
		}
		if !strings.Contains(res.ForLLM, "stdout:") {
			t.Errorf("expected 'stdout:' label before appended output, got %q", res.ForLLM)
		}
	})

	t.Run("blank stdout not appended", func(t *testing.T) {
		t.Parallel()
		res := sandboxDenialResult(1, "   ")
		if strings.Contains(res.ForLLM, "stdout:") {
			t.Errorf("expected no stdout section for blank stdout, got %q", res.ForLLM)
		}
	})

	t.Run("ForUser always empty", func(t *testing.T) {
		t.Parallel()
		res := sandboxDenialResult(0, "some stdout")
		if res.ForUser != "" {
			t.Errorf("ForUser must always be empty, got %q", res.ForUser)
		}
	})
}

// TestExecTool_SandboxDenial_UserFacingClean verifies that when the exec tool
// receives a fork-denial in stderr, the user-facing field (ForUser) does not
// contain the raw kernel string, and the LLM-facing field (ForLLM) contains the
// concise sanitized summary rather than the raw message.
//
// This test uses the legacy sandbox=off path (no sandbox.Run needed) and injects
// the denial via a command that writes the denial string to stderr and exits 1.
// That lets us verify the result-construction logic without requiring kernel
// sandbox infrastructure.
func TestExecTool_SandboxDenial_UserFacingClean(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only test")
	}
	t.Parallel()

	tool, err := NewExecTool("", false)
	if err != nil {
		t.Fatalf("NewExecTool: %v", err)
	}

	ctx, cancel := context.WithTimeout(WithToolContext(context.Background(), "cli", ""), 10*time.Second)
	defer cancel()

	// Simulate a fork-denial: write the raw kernel message to stderr and exit 1.
	res := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": `sh -c 'echo "Cannot fork" >&2; exit 1'`,
	})

	if !res.IsError {
		t.Fatalf("expected IsError=true for failed command")
	}

	// ForUser must not contain the raw denial string.
	if strings.Contains(res.ForUser, "Cannot fork") {
		t.Errorf("ForUser leaks raw kernel denial: %q", res.ForUser)
	}

	// ForLLM must be the sanitized summary (not the raw output).
	if strings.Contains(res.ForLLM, "Cannot fork") {
		t.Errorf("ForLLM leaks raw kernel denial: %q", res.ForLLM)
	}
	if !strings.Contains(res.ForLLM, "sandbox") {
		t.Errorf("ForLLM does not mention sandbox; expected sanitized summary, got: %q", res.ForLLM)
	}

	// FIX A2: exit code must be preserved in the LLM message even on the
	// blocked path so the model can reason about what was attempted.
	if !strings.Contains(res.ForLLM, "exited with code") {
		t.Errorf("ForLLM does not preserve exit code; got: %q", res.ForLLM)
	}

	// Guidance must be set.
	if res.Guidance == "" {
		t.Errorf("expected Guidance to be set for sandbox denial; got empty")
	}
}

// TestWorkspaceShellBg_SandboxDenial_UserFacingClean verifies that the F-1 fix
// is effective: when the workspace_shell_bg spawn path receives a fork/seccomp
// denial error string, summarizeSandboxDenial + sandboxDenialResult produce a
// ToolResult that:
//   - has IsError=true
//   - has ForUser empty (raw kernel text must not reach the user)
//   - has ForLLM containing the sandbox summary (not raw "Cannot fork")
//   - has Guidance set (do-not-retry signal)
//
// The test directly exercises the sanitization helper and the ToolResult
// constructor used in the F-1 fix path, without needing a live kernel sandbox.
func TestWorkspaceShellBg_SandboxDenial_UserFacingClean(t *testing.T) {
	t.Parallel()

	spawnErrors := []struct {
		name    string
		errStr  string
		wantHit bool
	}{
		{
			name:    "cannot fork from exec",
			errStr:  "fork/exec /bin/sh: Cannot fork",
			wantHit: true,
		},
		{
			name:    "fork EAGAIN resource unavailable",
			errStr:  "fork: Resource temporarily unavailable",
			wantHit: true,
		},
		{
			name:    "seccomp operation not permitted",
			errStr:  "seccomp: operation not permitted",
			wantHit: true,
		},
		{
			name:    "normal EPERM no spawn context — must NOT be treated as sandbox denial",
			errStr:  "open /etc/shadow: Operation not permitted",
			wantHit: false,
		},
	}

	for _, tc := range spawnErrors {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			summary, blocked := summarizeSandboxDenial(tc.errStr)
			if blocked != tc.wantHit {
				t.Fatalf("blocked=%v want %v (input=%q, summary=%q)", blocked, tc.wantHit, tc.errStr, summary)
			}
			if !blocked {
				return // non-denial: sanitization not applied, nothing further to check
			}

			// Simulate what the F-1 fix does: build the ToolResult from the summary.
			res := sandboxDenialResult(1, "")

			if !res.IsError {
				t.Errorf("expected IsError=true")
			}
			if res.ForUser != "" {
				t.Errorf("ForUser must be empty (kernel text must not reach user), got %q", res.ForUser)
			}
			if res.Guidance == "" {
				t.Errorf("expected Guidance to be set")
			}
			// The summary must not contain the raw kernel denial string.
			if strings.Contains(summary, "Cannot fork") {
				t.Errorf("summary leaks 'Cannot fork': %q", summary)
			}
			if strings.Contains(res.ForLLM, "Cannot fork") {
				t.Errorf("ForLLM leaks 'Cannot fork': %q", res.ForLLM)
			}
			// The ForLLM must mention 'sandbox'.
			if !strings.Contains(res.ForLLM, "sandbox") {
				t.Errorf("ForLLM does not mention sandbox; expected sanitized summary, got: %q", res.ForLLM)
			}
		})
	}
}

// TestExecTool_NormalFailure_PassesThrough verifies that a normal (non-sandbox)
// command failure still passes its real output through unchanged — only sandbox
// denial signatures are scrubbed.
func TestExecTool_NormalFailure_PassesThrough(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX-only test")
	}
	t.Parallel()

	tool, err := NewExecTool("", false)
	if err != nil {
		t.Fatalf("NewExecTool: %v", err)
	}

	ctx, cancel := context.WithTimeout(WithToolContext(context.Background(), "cli", ""), 10*time.Second)
	defer cancel()

	res := tool.Execute(ctx, map[string]any{
		"action":  "run",
		"command": "ls /nonexistent_directory_sanitize_test_12345",
	})

	if !res.IsError {
		t.Fatalf("expected IsError=true")
	}
	// The real "No such file" message must pass through to ForLLM.
	if !strings.Contains(strings.ToLower(res.ForLLM), "no such file") &&
		!strings.Contains(strings.ToLower(res.ForLLM), "cannot access") &&
		!strings.Contains(strings.ToLower(res.ForLLM), "not found") {
		t.Errorf("expected real error output in ForLLM, got: %q", res.ForLLM)
	}
	// Guidance must NOT be set for a normal failure.
	if res.Guidance != "" {
		t.Errorf("expected Guidance empty for normal failure, got: %q", res.Guidance)
	}
}
