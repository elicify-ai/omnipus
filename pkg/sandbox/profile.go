// Package sandbox — BuildLimits constructs the fixed Limits struct consumed by
// Run / ApplyChildHardening for bash's foreground-exec and background-session
// paths (ADR-036 merged the former workspace_shell / workspace_shell_bg tools
// into bash; see pkg/tools/shell.go).
//
// Design note (ADR-035-remove-per-agent-sandbox-profile): Omnipus previously
// offered a per-agent "sandbox profile" (workspace / workspace+net / host /
// off) that purported to select different kernel-enforcement strength per
// agent. In reality the Landlock/seccomp policy is installed ONCE, process-wide,
// at gateway boot (see seccomp_linux.go) and is inherited by every child
// regardless of the calling agent's selected profile — narrowing the filter
// per-child would require either a separate-process-per-child architecture or
// per-exec seccomp filter updates, both significant refactors this project's
// hard constraints (single Go binary, no CGo) rule out. The only field that
// genuinely varied by profile was whether the SSRF-protected egress proxy was
// injected into the child's environment; since that proxy is the SAME
// allow-list every other network-capable tool in the system already routes
// through, BuildLimits now injects it whenever the process-wide egress proxy
// is available (nil only if sandbox.NewEgressProxy failed at boot — see
// pkg/gateway/gateway.go, which degrades gracefully rather than failing boot
// in that case). This is the safer simplification, not a wider one: the old
// "workspace" profile's "no proxy" behavior meant raw HTTP from that child
// was never checked against the SSRF allow-list at all.
//
// The kernel sandbox (cwd confinement via Landlock, resource limits via
// ApplyChildHardening, audit logging) still applies to every invocation. The
// sole escape hatch is god mode (agent.GodModeActive), which the caller
// resolves once and threads through as an explicit bool — see
// pkg/tools/shell.go (ADR-036 merged the former workspace_shell.go /
// workspace_shell_bg.go into bash).

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"
)

// BuildLimits returns the sandbox.Limits every bash foreground-exec /
// background-session invocation (ADR-036 merge of the former workspace_shell /
// workspace_shell_bg) runs under: cwd confinement to workspaceDir,
// resource limits derived from timeoutSec, and the SSRF-protected egress
// proxy address (when proxy is non-nil). Kept as a named function (rather
// than inlined at call sites) so there is one place to change resource-limit
// defaults later.
func BuildLimits(workspaceDir string, proxy *EgressProxy, timeoutSec int32) (Limits, error) {
	wsDir, err := resolveWorkspaceDir(workspaceDir)
	if err != nil {
		return Limits{}, fmt.Errorf("sandbox.BuildLimits: %w", err)
	}
	proxyAddr := ""
	if proxy != nil {
		proxyAddr = proxy.Addr()
	}
	return Limits{
		TimeoutSeconds:   timeoutSec,
		MemoryLimitBytes: 0,
		WorkspaceDir:     wsDir,
		EgressProxyAddr:  proxyAddr,
	}, nil
}

// ResolveLimits returns the fixed Limits for a bash foreground-exec /
// background-session (ADR-036 merge of the former workspace_shell{,_bg})
// invocation, or the zero value when godMode is true. God mode bypasses
// hardening entirely, so BuildLimits' side effects (workspace mkdir, proxy
// resolution) are also skipped rather than computed and discarded — this
// matches the pre-ADR-035 behavior where the "off" profile short-circuited
// before any filesystem touch, and closes the asymmetry where the foreground
// path (then the separate workspace_shell tool, now bash's foreground mode)
// called BuildLimits unconditionally (a real MkdirAll that could fail on an
// empty/invalid workspaceDir even though its result was immediately
// overwritten) while the background path (then workspace_shell_bg, now
// bash's background mode) skipped it under god mode.
func ResolveLimits(godMode bool, workspaceDir string, proxy *EgressProxy, timeoutSec int32) (Limits, error) {
	if godMode {
		return Limits{}, nil
	}
	return BuildLimits(workspaceDir, proxy, timeoutSec)
}

// resolveWorkspaceDir returns an absolute, clean workspace path, creating the
// directory if it does not exist. Returns an error when workspaceDir is empty
// or cannot be resolved.
func resolveWorkspaceDir(workspaceDir string) (string, error) {
	if workspaceDir == "" {
		return "", fmt.Errorf("workspaceDir is empty")
	}
	abs, err := filepath.Abs(workspaceDir)
	if err != nil {
		return "", fmt.Errorf("resolve workspace path: %w", err)
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return "", fmt.Errorf("create workspace dir %s: %w", abs, err)
	}
	return abs, nil
}
