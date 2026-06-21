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
	// DispatchKindExternalCLI means the task is delegated to an external CLI runner
	// (Claude Code, Codex, opencode). ACTIVE in v0.1.0: ResolveDispatch returns this
	// kind for Kind="external-cli". The run executes in a git-worktree-isolated dir
	// under the external CLI's OWN sandbox (Codex = Landlock/seccomp + Seatbelt;
	// Claude Code = its permission model); Omnipus adds no new confiner. Consent is
	// routed best-effort post-hoc (the CLI's own sandbox is the real boundary).
	DispatchKindExternalCLI DispatchKind = "external-cli"
)

// ErrRemoteA2AReserved is returned when an executor with Kind="remote-a2a" is
// dispatched. The kind is accepted in the schema for forward-compatibility but is
// not resolvable in v0.1.0.
var ErrRemoteA2AReserved = fmt.Errorf(
	"executor kind %q is reserved and not available in v0.1.0",
	config.ExecutorKindRemoteA2A,
)

// NewDriver constructs the ExternalAgentRunner driver for the given external CLI
// name (the config.ExecutorConfig.CLI value: "claude-code", "codex", "opencode").
// consent may be nil; when nil every permission request is denied by default.
// An unknown/empty cli name returns an error so the dispatch site can fail the run
// cleanly rather than silently delegating nowhere.
func NewDriver(cli string, consent ConsentHandler) (ExternalAgentRunner, error) {
	switch cli {
	case "claude-code":
		// "claude-code" is the canonical config value (matches conntest.go
		// supportedCLIs and the AgentExecutorCli enum). No "claude" alias — the
		// run path and the connection-test path must agree on the same key.
		return NewClaudeDriver(consent), nil
	case "codex":
		return NewCodexDriver(consent), nil
	case "opencode":
		return NewOpencodeDriver(consent), nil
	default:
		return nil, fmt.Errorf("unknown external CLI %q (supported: %s)", cli, "claude-code, codex, opencode")
	}
}

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
//   - Kind="external-cli"             → DispatchKindExternalCLI, nil (ACTIVE in v0.1.0)
//   - Kind="remote-a2a"              → "", ErrRemoteA2AReserved
//   - any other unknown kind          → "", error
//
// external-cli is ACTIVE in v0.1.0: the dispatch site (pkg/agent/subturn.go) runs
// the external run in a git-worktree-isolated directory under the external CLI's
// OWN sandbox (the operator decision — Omnipus adds no new kernel confiner). The
// CLI's permission prompts are routed to the Omnipus consent layer best-effort
// post-hoc (the CLI's own sandbox + the worktree are the authoritative boundary —
// see consent.go's POST-HOC CONSENT note).
//
// This function is the single dispatch gate. All sub-agent dispatch sites MUST
// call it before choosing an execution path.
func ResolveDispatch(ec *config.ExecutorConfig) (DispatchKind, error) {
	switch ec.EffectiveKind() {
	case config.ExecutorKindNative:
		return DispatchKindNative, nil
	case config.ExecutorKindExternalCLI:
		return DispatchKindExternalCLI, nil
	case config.ExecutorKindRemoteA2A:
		return "", ErrRemoteA2AReserved
	default:
		return "", fmt.Errorf("unknown executor kind %q", ec.EffectiveKind())
	}
}
