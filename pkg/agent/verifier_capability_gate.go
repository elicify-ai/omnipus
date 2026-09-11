// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
//
// Copyright (c) 2026 Omnipus contributors

package agent

// VerifierGodModeRefusalReasonPrefix is the machine-readable reason prefix
// JUDGE-FR-057/FR-057a require on a god-mode adjudication refusal. Carried
// onto the goal-status frame / task run record by later waves (not only
// logged), so the operator sees a distinct, actionable state rather than one
// indistinguishable from a provider outage — see FR-057a.
const VerifierGodModeRefusalReasonPrefix = "god_mode: "

// VerifierGodModeRefusalReason is the JUDGE-FR-057 capability-gate check: a
// pure, side-effect-free predicate over the single fact that decides it
// (whether god mode is enabled), returning the exact machine-readable reason
// FR-057 requires and whether the caller must refuse.
//
// godMode floors every tool at "allow" (Constraint #6 sandbox-off posture),
// which defeats systemAgentSeed's verifier tool-policy ceiling entirely —
// the Judge's narrow read-only + verification surface (read_file,
// list_directory, inspect_session) becomes indistinguishable from full
// access, and FR-058's "mcp_*": deny stamp resolves no protection at all
// under god mode (resolveEffectivePolicyWith short-circuits on cfg.GodMode
// before the per-agent map is ever consulted). An adjudication run under
// that posture cannot be trusted, so it MUST be refused before a verifier
// session is even created — not merely denied tool-by-tool once running.
//
// This function decides the refusal in isolation, testable without a real
// verifier session, a real Judge instance, or any I/O. The caller —
// runVerifierAdjudication (pkg/agent/judge.go, wave E3/E9's region) — is
// responsible for: (a) calling this BEFORE creating a verifier session, per
// FR-057's "MUST refuse before creating a verifier session, MUST NOT run a
// Judge turn" requirement, and (b) carrying the returned reason onto the
// goal-status frame and the task run record, per FR-057a — this function
// does not reach either surface itself.
func VerifierGodModeRefusalReason(godMode bool) (reason string, refuse bool) {
	if !godMode {
		return "", false
	}
	return VerifierGodModeRefusalReasonPrefix + "adjudication refused because god mode floors every tool at allow", true
}
