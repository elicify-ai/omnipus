// License: MIT
// Copyright (c) 2026 Omnipus contributors
package integration

import (
	"testing"

	"github.com/elicify-ai/omnipus/pkg/agent/testutil"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// TestSandboxAuditHooks_WiredAtBootClearedAtShutdown proves the sandbox →
// audit bridges are REACHABLE, not merely defined. pkg/sandbox's own tests
// show each emitter writes a correct entry once a hook is installed; they
// cannot show the gateway installs it. SetGitDenyAuditHook had no caller
// from ADR-053 D17 until 2026-09-12, so 182 evidence-repo denials in one CI
// run reached slog only and never the audit trail.
//
// DIES ON: dropping either Set*AuditHook call from wireSandboxAuditHooks, or
// dropping the wireSandboxAuditHooks call from the gateway boot path, or
// dropping unwireSandboxAuditHooks from shutdown.
func TestSandboxAuditHooks_WiredAtBootClearedAtShutdown(t *testing.T) {
	// A hook left behind by an earlier in-process gateway would make the
	// "wired after boot" half pass vacuously; start from a known-clear state.
	sandbox.SetRestrictAuditHook(nil)
	sandbox.SetGitDenyAuditHook(nil)
	if sandbox.GitDenyAuditHookWired() || sandbox.RestrictAuditHookWired() {
		t.Fatal("precondition: hooks must be clear before boot")
	}

	tg := testutil.StartTestGateway(t)

	if !sandbox.GitDenyAuditHookWired() {
		t.Error("git-evidence deny audit hook is NOT wired after gateway boot — denials fall to the slog fallback and never reach the audit trail")
	}
	if !sandbox.RestrictAuditHookWired() {
		t.Error("per-thread restrict-failure audit hook is NOT wired after gateway boot")
	}

	tg.Close()

	if sandbox.GitDenyAuditHookWired() || sandbox.RestrictAuditHookWired() {
		t.Error("sandbox audit hooks must be cleared at shutdown so a torn-down gateway's closure does not outlive it")
	}
}
