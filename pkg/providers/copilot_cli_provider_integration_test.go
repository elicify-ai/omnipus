//go:build integration

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package providers

import (
	"context"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// These tests drive the REAL GitHub Copilot CLI and therefore spend the
// operator's premium-request quota. They are gated behind the `integration`
// build tag and skip when `copilot` is not installed.
//
//	go test -tags='goolm stdjson integration' -run TestIntegration_RealCopilot ./pkg/providers/
func TestIntegration_RealCopilotCLI(t *testing.T) {
	path, err := exec.LookPath(CopilotCLICommand)
	if err != nil {
		t.Skip("copilot CLI not found on PATH, skipping integration test")
	}
	t.Logf("using copilot CLI at: %s", path)

	p := NewCopilotCliProvider(t.TempDir())

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp, err := p.Chat(ctx, []Message{
		{Role: "user", Content: "Respond with only the word 'pong'. Nothing else."},
	}, nil, "", nil)
	if err != nil {
		t.Fatalf("Chat() with the real CLI error = %v", err)
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if !strings.Contains(strings.ToLower(resp.Content), "pong") {
		t.Errorf("Content = %q, expected it to contain 'pong'", resp.Content)
	}
}

// TestIntegration_RealCopilotCLI_SignInStatus checks the status probe against
// the operator's own installation — the one place the flag contract pinned by
// the fake binary can be falsified.
func TestIntegration_RealCopilotCLI_SignInStatus(t *testing.T) {
	if _, err := exec.LookPath(CopilotCLICommand); err != nil {
		t.Skip("copilot CLI not found on PATH")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	st := CopilotSignIn(ctx, "", t.TempDir())
	t.Logf("copilot sign-in status: state=%s account=%q", st.State, st.AccountLabel)

	switch st.State {
	case CopilotSignedIn, CopilotNotSignedIn, CopilotSignInExpired:
	default:
		t.Errorf("state = %q, want one of signed_in|not_signed_in|expired with the CLI installed", st.State)
	}
}
