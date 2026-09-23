// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package gateway

import (
	"net/http"

	gen "github.com/elicify-ai/omnipus/pkg/api/generated"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// rest_security_wave5.go — Wave 5 operator-facing REST endpoint (SEC-01/02/03).
//
// GET /api/v1/security/sandbox-status returns the active sandbox backend,
// its capabilities (Landlock ABI version, blocked syscalls, kernel vs fallback),
// and whether seccomp filtering is active.

// HandleSandboxStatus handles GET /api/v1/security/sandbox-status.
//
// Sprint-J: the response now includes the resolved Mode, DisabledBy, and
// Landlock/Seccomp enforcement flags so operators can distinguish enforce
// from permissive (audit-only) from off (disabled) states. FR-J-008 and
// the BDD scenario "Fresh boot applies Landlock and seccomp" both verify
// the "Apply() has not been called" note is gone after a successful wire.
func (a *restAPI) HandleSandboxStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		jsonErr(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	// Guard against a nil agentLoop rather than relying on method dispatch
	// safety. This matches the pattern in rest_security_wave3.go and keeps
	// the handler honest during startup windows and test harnesses.
	if a.agentLoop == nil {
		jsonErr(w, http.StatusServiceUnavailable, "sandbox: agent loop not initialized")
		return
	}
	backend := a.agentLoop.SandboxBackend()
	// Sprint-J: enrich the response with gateway-owned state (mode,
	// disabled_by, landlock_enforced, seccomp_enforced, audit_only).
	// When sandboxResult is nil (legacy path or test harness that skipped
	// applySandbox), fall back to the bare backend description — the
	// response will have the same shape but with Mode empty.
	var state sandbox.ApplyState
	var bindCount int
	if a.sandboxResult != nil {
		state = a.sandboxResult.ApplyState
		bindCount = len(a.sandboxResult.Policy.BindPortRules)
	}
	status := sandbox.DescribeBackendWithState(backend, state)
	// bind_ports_count lets operators curl this endpoint and verify the bind
	// allow-list is the size they expect (per cfg.Sandbox.DevServerPortRange).
	// Zero on FallbackBackend, on Mode=Off, and on Landlock ABI < 4 — exactly
	// the cases where no kernel net rules were installed.
	//
	// connect_ports_count was removed in v0.1 (A1.3): the kernel never
	// enforced connect-port rules (NET_CONNECT_TCP not in handledAccessNet),
	// so advertising a count was misleading. Outbound TCP filtering is
	// handled by the egress proxy.
	resp := sandboxStatusToWire(status, bindCount)

	// ADR-092 (review finding A): the three inputs the chat badge and the
	// composer's Auto switch read. kernel_sandbox_active is the very
	// predicate the agent loop uses to choose Auto vs Ask
	// (ShellPermissionGate.liveMode -> sandbox.TurnPolicyBaseInstalled);
	// auto_approve_effective is the raw global default, deliberately NOT
	// ANDed with it (the SPA renders "Auto -> Ask" from the pair);
	// god_mode_active mirrors GodModeStatus.enabled.
	cfg := a.agentLoop.GetConfig()
	kernelActive := sandbox.TurnPolicyBaseInstalled()
	autoApprove := cfg != nil && cfg.Sandbox.AutoApprove
	godModeActive := cfg != nil && cfg.Sandbox.GodMode && a.godModeAvailable()
	resp.KernelSandboxActive = &kernelActive
	resp.AutoApproveEffective = &autoApprove
	resp.GodModeActive = &godModeActive
	jsonOK(w, resp)
}

// sandboxStatusToWire maps the sandbox package's runtime description onto
// the generated SandboxStatus wire type (Hard Constraint #8). Optional
// members keep their previous omit-when-zero shape — the SPA tests
// abi_version against null, for instance — so the only additions on the wire
// are the ADR-092 fields HandleSandboxStatus sets itself.
func sandboxStatusToWire(status sandbox.Status, bindCount int) gen.SandboxStatus {
	out := gen.SandboxStatus{
		Backend:        status.Backend,
		Available:      status.Available,
		KernelLevel:    status.KernelLevel,
		PolicyApplied:  status.PolicyApplied,
		SeccompEnabled: status.SeccompEnabled,
		BindPortsCount: bindCount,
	}
	if status.ABIVersion != 0 {
		v := status.ABIVersion
		out.AbiVersion = &v
	}
	if status.BlockedSyscalls != nil {
		v := append([]string(nil), status.BlockedSyscalls...)
		out.BlockedSyscalls = &v
	}
	if status.LandlockFeatures != nil {
		v := append([]string(nil), status.LandlockFeatures...)
		out.LandlockFeatures = &v
	}
	if status.Notes != nil {
		v := append([]string(nil), status.Notes...)
		out.Notes = &v
	}
	if status.FilesystemModel != "" {
		v := gen.SandboxStatusFilesystemModel(status.FilesystemModel)
		out.FilesystemModel = &v
	}
	if status.Mode != "" {
		v := string(status.Mode)
		out.Mode = &v
	}
	if status.DisabledBy != "" {
		v := status.DisabledBy
		out.DisabledBy = &v
	}
	out.LandlockEnforced = trueOrNil(status.LandlockEnforced)
	out.SeccompEnforced = trueOrNil(status.SeccompEnforced)
	out.AuditOnly = trueOrNil(status.AuditOnly)
	return out
}

// trueOrNil returns a pointer to true, or nil for false — the omit-when-false
// shape these three optional flags have always had on the wire.
func trueOrNil(b bool) *bool {
	if !b {
		return nil
	}
	t := true
	return &t
}
