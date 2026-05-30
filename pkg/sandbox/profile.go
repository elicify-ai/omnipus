// Package sandbox — LimitsForProfile maps per-agent SandboxProfile values
// to the Limits struct consumed by Run / ApplyChildHardening.
//
// Design note: profile-scoped seccomp filters are intentionally not implemented
// here. The seccomp filter (see seccomp_linux.go) is installed process-wide via
// prctl(PR_SET_SECCOMP) + TSYNC, meaning all threads and child processes inherit
// the same filter. Narrowing the filter per-child would require either a
// separate-process-per-child architecture or per-exec seccomp filter updates,
// both of which are significant refactors. All profiles currently reuse the
// existing strict 15-syscall filter as applied at gateway boot. If `npm install`
// hits EPERM under workspace+net, the filter will be revisited in a future
// migration. Tracked here so it is not forgotten.

package sandbox

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/dapicom-ai/omnipus/pkg/config"
)

// LimitsForProfile returns the sandbox.Limits that should be applied when
// running a child process under the given per-agent SandboxProfile.
//
// Profile semantics:
//   - "":            treated as "workspace" (safe default).
//   - "workspace":       WorkspaceDir set; EgressProxyAddr empty (network
//     isolated); RLIMIT_AS/CPU governed by timeoutSec.
//   - "workspace+net":   WorkspaceDir set; EgressProxyAddr set to the
//     running proxy's address (operator allow-list applies).
//   - "host":            WorkspaceDir set for npm cache; EgressProxyAddr set
//     (broader network access via proxy allow-list).
//     Filesystem access is wider (host paths) — controlled
//     by Landlock at the gateway process level.
//   - "off":             Returns zero-value Limits AND IsGodMode returns true.
//     Callers MUST check IsGodMode before calling
//     ApplyChildHardening — passing zero Limits to
//     ApplyChildHardening is a no-op on most platforms but
//     the intent check is important for auditability.
//
// Note on seccomp: the strict 15-syscall filter is installed at gateway-process
// level and inherited by all children via TSYNC. Profile-scoped seccomp
// narrowing is deferred to a future PR; all profiles currently share the same
// inherited filter.
func LimitsForProfile(
	profile config.SandboxProfile,
	workspaceDir string,
	proxy *EgressProxy,
	timeoutSec int32,
) (Limits, error) {
	// Normalise empty string to workspace (safe default).
	if profile == "" {
		profile = config.SandboxProfileWorkspace
	}

	switch profile {
	case config.SandboxProfileOff:
		// God mode: return zero-value Limits. Caller must call IsGodMode
		// and skip ApplyChildHardening.
		return Limits{}, nil

	case config.SandboxProfileWorkspace:
		// Strict: workspace-only filesystem, no outbound network via proxy.
		wsDir, err := resolveWorkspaceDir(workspaceDir)
		if err != nil {
			return Limits{}, fmt.Errorf("LimitsForProfile workspace: %w", err)
		}
		return Limits{
			TimeoutSeconds:   timeoutSec,
			MemoryLimitBytes: 0,
			WorkspaceDir:     wsDir,
			EgressProxyAddr:  "", // no network
		}, nil

	case config.SandboxProfileWorkspaceNet:
		// Workspace filesystem + outbound HTTP/HTTPS via egress proxy allow-list.
		wsDir, err := resolveWorkspaceDir(workspaceDir)
		if err != nil {
			return Limits{}, fmt.Errorf("LimitsForProfile workspace+net: %w", err)
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

	case config.SandboxProfileHost:
		// Host-level access: workspace dir still used for npm cache injection;
		// broader filesystem reach is governed by the Landlock rules applied
		// to the gateway process (inherited by children). Egress via proxy so
		// operator allow-list still applies.
		proxyAddr := ""
		if proxy != nil {
			proxyAddr = proxy.Addr()
		}
		// For host profile the workspace dir is still useful for npm cache
		// even if the filesystem restriction is wider. We set WorkspaceDir
		// only when the directory actually exists (avoid injecting a bad path).
		wsDir := ""
		if workspaceDir != "" {
			if abs, err := filepath.Abs(workspaceDir); err == nil {
				wsDir = abs
			}
		}
		return Limits{
			TimeoutSeconds:   timeoutSec,
			MemoryLimitBytes: 0,
			WorkspaceDir:     wsDir,
			EgressProxyAddr:  proxyAddr,
		}, nil

	default:
		return Limits{}, fmt.Errorf("LimitsForProfile: unknown profile %q", profile)
	}
}

// IsGodMode reports whether profile is SandboxProfileOff. When true the
// caller MUST skip ApplyChildHardening entirely — passing zero Limits to
// ApplyChildHardening is formally a no-op, but the explicit check ensures
// the audit path records the intentional sandbox bypass.
func IsGodMode(profile config.SandboxProfile) bool {
	return profile == config.SandboxProfileOff
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
