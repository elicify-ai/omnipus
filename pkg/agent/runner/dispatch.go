package runner

import (
	"fmt"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// DispatchKind classifies how a sub-agent task should be executed.
type DispatchKind string

const (
	// DispatchKindNative means the task runs inside the Omnipus agent loop (default).
	DispatchKindNative DispatchKind = "native"
	// DispatchKindExternalCLI means the task is delegated to an external CLI runner.
	// RESERVED/experimental in v0.1.0: ResolveDispatch never returns this kind — it
	// returns ErrExternalCLINotWired instead (external-cli consent is post-hoc, so
	// it is not wired into production dispatch). The constant is retained for the
	// drivers and for the future wired path.
	DispatchKindExternalCLI DispatchKind = "external-cli"
)

// ErrRemoteA2AReserved is returned when an executor with Kind="remote-a2a" is
// dispatched. The kind is accepted in the schema for forward-compatibility but is
// not resolvable in v0.1.0.
var ErrRemoteA2AReserved = fmt.Errorf("executor kind %q is reserved and not available in v0.1.0", config.ExecutorKindRemoteA2A)

// ErrExternalCLINotWired is returned when Kind="external-cli" is dispatched.
// external-cli is RESERVED/experimental in v0.1.0 and intentionally not wired
// into production dispatch (post-hoc consent — see consent.go). Setting it is a
// documented no-op that surfaces this sentinel, not a silent delegation.
var ErrExternalCLINotWired = fmt.Errorf("executor kind %q is reserved and not yet wired in v0.1.0", config.ExecutorKindExternalCLI)

// DenyByDefault constructs a denial PermissionDecision for use when no consent
// handler is registered. This is the deny-by-default gate required by FR-5.1.
func DenyByDefault(requestID string) PermissionDecision {
	return PermissionDecision{
		RequestID: requestID,
		Allow:     false,
		Reason:    "no consent handler registered — denied by default",
	}
}

// ResolveDispatch inspects the ExecutorConfig and returns the DispatchKind that
// should be used to run a sub-agent task.
//
//   - nil or Kind="" or Kind="native" → DispatchKindNative, nil (existing path unchanged)
//   - Kind="external-cli"             → "", ErrExternalCLINotWired (RESERVED in v0.1.0)
//   - Kind="remote-a2a"              → "", ErrRemoteA2AReserved
//   - any other unknown kind          → "", error
//
// external-cli is RESERVED/experimental in v0.1.0 and is intentionally NOT wired
// into production sub-agent dispatch: its consent is fundamentally post-hoc (the
// CLI runs a tool before Omnipus can gate it — see consent.go's POST-HOC CONSENT
// LIMITATION). Setting Kind="external-cli" is therefore a documented no-op that
// surfaces ErrExternalCLINotWired at dispatch, exactly like remote-a2a, rather
// than silently delegating to an un-gated external process. The streaming drivers
// (ClaudeDriver/CodexDriver/OpencodeDriver) remain in-tree and correct for when a
// genuinely pre-emptive consent path lets external-cli be wired later.
//
// This function is the single dispatch gate. All sub-agent dispatch sites MUST
// call it before choosing an execution path.
func ResolveDispatch(ec *config.ExecutorConfig) (DispatchKind, error) {
	switch ec.EffectiveKind() {
	case config.ExecutorKindNative:
		return DispatchKindNative, nil
	case config.ExecutorKindExternalCLI:
		return "", ErrExternalCLINotWired
	case config.ExecutorKindRemoteA2A:
		return "", ErrRemoteA2AReserved
	default:
		return "", fmt.Errorf("unknown executor kind %q", ec.EffectiveKind())
	}
}
