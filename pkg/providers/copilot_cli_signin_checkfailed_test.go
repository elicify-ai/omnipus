// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestCopilotCliSignIn_CheckThatCannotRunIsNotASignInState covers a sign-in
// check whose CLI run never answered the question at all. Neither case is a
// statement about the login, so neither may be classified into one.
//
// Both used to be: a launch failure's Go error names the binary's path (here a
// folder called tools-401, which the old bare "401" marker read as an expired
// session), and a timeout's "signal: killed" fell through to not_signed_in
// with a generic "unrecognised message" warning.
func TestCopilotCliSignIn_CheckThatCannotRunIsNotASignInState(t *testing.T) {
	skipOnWindows(t)

	t.Run("the binary cannot be started", func(t *testing.T) {
		dir := filepath.Join(t.TempDir(), "tools-401")
		if err := os.Mkdir(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		script := filepath.Join(dir, "copilot")
		// Executable and found, but its interpreter does not exist: the kernel
		// refuses to start it, so there is no exit code and no stderr.
		if err := os.WriteFile(script, []byte("#!/nonexistent/omnipus-test-interpreter\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		got := CopilotSignIn(context.Background(), script, "")
		if got.State != CopilotCheckFailed {
			t.Fatalf("state = %q, want %q (detail %q)", got.State, CopilotCheckFailed, got.Detail)
		}
		if !strings.Contains(got.Detail, "could not start") {
			t.Errorf("detail = %q, want it to say the CLI could not start", got.Detail)
		}
	})

	t.Run("the check times out", func(t *testing.T) {
		script := filepath.Join(t.TempDir(), "copilot")
		// `exec` replaces bash with sleep, so the timeout's kill reaches the
		// process holding the output pipes and Run returns promptly.
		if err := os.WriteFile(script, []byte("#!/bin/bash\nexec sleep 5\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
		defer cancel()

		got := CopilotSignIn(ctx, script, "")
		if got.State != CopilotCheckFailed {
			t.Fatalf("state = %q, want %q (detail %q)", got.State, CopilotCheckFailed, got.Detail)
		}
		if !strings.Contains(got.Detail, "did not finish") {
			t.Errorf("detail = %q, want it to say the check did not finish", got.Detail)
		}
	})
}
