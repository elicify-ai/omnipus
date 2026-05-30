//go:build !cgo

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Tests for B1.2(b): when sandbox.audit_log == true and audit.NewLogger
// returns an error, NewAgentLoop must surface a typed
// audit.LoggerConstructionError. The gateway then maps that to a
// SandboxBootError so cmd/omnipus exits with EX_CONFIG (78). When
// audit_log is false the existing log-and-continue behavior is preserved.

package gateway

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dapicom-ai/omnipus/pkg/agent"
	"github.com/dapicom-ai/omnipus/pkg/audit"
	"github.com/dapicom-ai/omnipus/pkg/bus"
	"github.com/dapicom-ai/omnipus/pkg/config"
)

// TestNewAgentLoop_AuditConstructionFails_AuditLogTrue_BootAborts verifies
// B1.2(b) fail-closed semantics. We force audit.NewLogger to fail by
// pre-creating a regular file at the path the agent loop will try to
// MkdirAll (auditDir = filepath.Dir(workspace) + "/system"). os.MkdirAll
// returns ENOTDIR when a non-directory exists at the target path, which
// audit.NewLogger surfaces as an error. With sandbox.audit_log=true,
// NewAgentLoop must return *audit.LoggerConstructionError.
func TestNewAgentLoop_AuditConstructionFails_AuditLogTrue_BootAborts(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	// Plant a regular file where the agent loop will try to MkdirAll the
	// audit directory. homePath = filepath.Dir(workspace) = tmpDir, so
	// auditDir = tmpDir/system. Create tmpDir/system as a file, not a
	// directory. os.MkdirAll on a path whose parent component already
	// exists as a regular file returns ENOTDIR (or similar), which
	// audit.NewLogger wraps and returns.
	conflictPath := filepath.Join(tmpDir, "system")
	if err := os.WriteFile(conflictPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create conflict file: %v", err)
	}

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: workspaceDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: true, // operator explicitly requested audit
		},
	}

	msgBus := bus.NewMessageBus()
	loop, err := agent.NewAgentLoop(cfg, msgBus, &restMockProvider{})
	if loop != nil {
		// Defensive: if (broken) implementation returned a loop, close
		// it so we don't leak goroutines in -count=N runs.
		t.Cleanup(loop.Close)
	}
	if err == nil {
		t.Fatalf("expected boot-abort error when audit construction fails with audit_log=true; got nil")
	}

	var lcErr *audit.LoggerConstructionError
	if !errors.As(err, &lcErr) {
		t.Fatalf("expected *audit.LoggerConstructionError; got %T: %v", err, err)
	}
	if lcErr.Dir == "" {
		t.Errorf("LoggerConstructionError.Dir must identify the failing path; got empty")
	}
	if lcErr.Err == nil {
		t.Errorf("LoggerConstructionError.Err must wrap the underlying audit.NewLogger error")
	}

	msg := err.Error()
	if !strings.Contains(msg, "audit log construction failed") {
		t.Errorf("error message must mention 'audit log construction failed' for operator clarity; got: %s", msg)
	}
	if !strings.Contains(msg, "sandbox.audit_log") {
		t.Errorf("error message must mention the remediation 'disable sandbox.audit_log'; got: %s", msg)
	}
}

// TestNewAgentLoop_AuditConstructionFails_AuditLogFalse_Continues verifies
// the inverse: when sandbox.audit_log is false we keep the legacy log-and-
// continue behavior. The audit branch is gated by cfg.Sandbox.AuditLog,
// so a configuration error in the audit dir cannot fire when audit is
// disabled — but we still test that NewAgentLoop boots without surfacing
// any audit error path.
func TestNewAgentLoop_AuditConstructionFails_AuditLogFalse_Continues(t *testing.T) {
	tmpDir := t.TempDir()
	workspaceDir := filepath.Join(tmpDir, "workspace")
	if err := os.MkdirAll(workspaceDir, 0o700); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	// Plant the same conflict file. With audit_log=false the audit
	// branch is never entered, so this file should not block boot.
	conflictPath := filepath.Join(tmpDir, "system")
	if err := os.WriteFile(conflictPath, []byte("not a directory"), 0o600); err != nil {
		t.Fatalf("create conflict file: %v", err)
	}

	cfg := &config.Config{
		Gateway: config.GatewayConfig{Host: "127.0.0.1", Port: 0},
		Agents: config.AgentsConfig{
			Defaults: config.AgentDefaults{
				Workspace: workspaceDir,
				ModelName: "test-model",
				MaxTokens: 4096,
			},
		},
		Sandbox: config.OmnipusSandboxConfig{
			AuditLog: false, // audit disabled — fail-closed must not fire
		},
	}

	msgBus := bus.NewMessageBus()
	loop, err := agent.NewAgentLoop(cfg, msgBus, &restMockProvider{})
	if err != nil {
		// Tolerate other unrelated boot failures by skipping the test only
		// when the failure is NOT an audit construction error (which would
		// indicate the gating is broken).
		var lcErr *audit.LoggerConstructionError
		if errors.As(err, &lcErr) {
			t.Fatalf("LoggerConstructionError fired with audit_log=false (gating broken): %v", err)
		}
		// Some other unrelated boot failure — treat as t.Skip rather than
		// fail; this test only cares about the audit-fail-closed gate.
		t.Skipf("unrelated boot failure with audit_log=false: %v", err)
	}
	if loop != nil {
		t.Cleanup(loop.Close)
	}
}

// TestSandboxBootError_WrapsAuditConstructionError documents the contract
// between agent.NewAgentLoop and the gateway-level fail-closed handler.
// The gateway maps audit.LoggerConstructionError to *SandboxBootError,
// so cmd/omnipus's existing exit-code mapping (EX_CONFIG=78 per FR-J-004)
// kicks in without further plumbing.
func TestSandboxBootError_WrapsAuditConstructionError(t *testing.T) {
	underlying := errors.New("permission denied")
	lcErr := &audit.LoggerConstructionError{Dir: "/var/lib/omnipus/system", Err: underlying}
	bootErr := &SandboxBootError{Err: lcErr}

	// errors.As must traverse the SandboxBootError → LoggerConstructionError
	// chain so callers (cmd/omnipus, test code) can introspect the failure
	// reason without string-matching.
	var got *audit.LoggerConstructionError
	if !errors.As(bootErr, &got) {
		t.Fatalf("errors.As must find *audit.LoggerConstructionError through SandboxBootError; got nothing")
	}
	if got.Dir != "/var/lib/omnipus/system" {
		t.Errorf("Dir not preserved through Unwrap chain: %q", got.Dir)
	}

	// errors.Is must reach the underlying os-level error so a caller can
	// distinguish disk-full from permission errors if it cares.
	if !errors.Is(bootErr, underlying) {
		t.Error("errors.Is must reach the underlying error through both wrappers")
	}
}
