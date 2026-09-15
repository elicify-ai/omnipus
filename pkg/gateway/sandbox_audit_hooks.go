// Omnipus — sandbox → audit bridges wired at gateway boot
// License: MIT
// Copyright (c) 2026 Omnipus contributors
package gateway

import (
	"log/slog"

	"github.com/elicify-ai/omnipus/pkg/agent"
	"github.com/elicify-ai/omnipus/pkg/audit"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// wireSandboxAuditHooks installs every sandbox-side audit emitter the gateway
// is responsible for. The sandbox package cannot import the agent loop (cycle),
// so it exposes one setter per emitter and the gateway bridges each to the
// loop's audit logger here — in ONE place, so a new setter cannot be added to
// pkg/sandbox and forgotten on the gateway side again.
//
// That is not hypothetical: SetGitDenyAuditHook shipped with ADR-053 D17 and
// had no caller until 2026-09-12. A CI run that day denied `git commit` in a
// protected evidence repo 182 times; every denial fell to the slog fallback
// ("no audit hook wired") and none reached the audit trail. The integration
// test tests/integration/sandbox_audit_hooks_wired_test.go now proves both
// hooks are live after boot and cleared after shutdown.
//
// Both setters are idempotent, so re-wiring across hot reloads and the
// in-process test harness is safe.
func wireSandboxAuditHooks(al *agent.AgentLoop) {
	sandbox.SetRestrictAuditHook(sandboxAuditBridge(al, "per-thread restrict failed"))
	sandbox.SetGitDenyAuditHook(sandboxAuditBridge(al, "git-evidence sandbox block"))
}

// unwireSandboxAuditHooks clears every hook wireSandboxAuditHooks installed.
// Called from shutdown so a torn-down gateway's closure (which captures the
// agent loop) cannot outlive it.
func unwireSandboxAuditHooks() {
	sandbox.SetRestrictAuditHook(nil)
	sandbox.SetGitDenyAuditHook(nil)
}

// sandboxAuditBridge returns an emitter that writes the entry to al's audit
// logger, logging (never dropping silently) when the loop or its logger is
// absent. B1.2(a): logger.Log is nil-safe on the entry.
func sandboxAuditBridge(al *agent.AgentLoop, what string) func(*audit.Entry) {
	return func(entry *audit.Entry) {
		if al == nil {
			slog.Error("sandbox: "+what+" (no agent loop; audit entry dropped)",
				"event", entry.Event, "details", entry.Details)
			return
		}
		logger := al.AuditLogger()
		if logger == nil {
			slog.Error("sandbox: "+what+" (audit logger disabled)",
				"event", entry.Event, "details", entry.Details)
			return
		}
		if logErr := logger.Log(entry); logErr != nil {
			slog.Error("sandbox: "+what+" audit write failed",
				"event", entry.Event, "error", logErr)
		}
	}
}
