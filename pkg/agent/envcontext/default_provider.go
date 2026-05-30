package envcontext

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/dapicom-ai/omnipus/pkg/config"
	"github.com/dapicom-ai/omnipus/pkg/sandbox"
)

// DefaultProvider is the production implementation of Provider. It derives
// all values from runtime state: config, sandbox backend, and process environment.
// Constructor: NewDefaultProvider.
type DefaultProvider struct {
	cfg       *config.Config
	backend   sandbox.SandboxBackend
	workspace string
}

// NewDefaultProvider constructs a DefaultProvider from the live config, the
// active sandbox backend, and the agent workspace.
//
// cfg MUST be non-nil — the env preamble's NetworkPolicy and ActiveWarnings
// fields both read live config values, and a nil cfg would silently produce a
// misleading preamble (outbound-denied, no warnings). We panic at construction
// time so wiring bugs surface during boot rather than at first agent turn.
//
// backend may be nil — DescribeBackend handles that explicitly.
// workspace should be an absolute path; the caller is responsible for
// resolving it before construction.
func NewDefaultProvider(cfg *config.Config, backend sandbox.SandboxBackend, workspace string) *DefaultProvider {
	if cfg == nil {
		panic("envcontext.NewDefaultProvider: cfg must not be nil")
	}
	return &DefaultProvider{
		cfg:       cfg,
		backend:   backend,
		workspace: workspace,
	}
}

// procVersionOnce ensures /proc/version is read at most once per process lifetime.
var (
	procVersionOnce sync.Once
	cachedKernel    string
	errCachedKernel error
)

// readKernelVersion reads /proc/version once and extracts the short release
// token (e.g., "6.8"). The result is cached for the process lifetime.
// On non-Linux systems this returns ("", nil).
// On read failure it returns ("", err).
func readKernelVersion() (string, error) {
	if runtime.GOOS != "linux" {
		return "", nil
	}
	procVersionOnce.Do(func() {
		data, err := os.ReadFile("/proc/version")
		if err != nil {
			errCachedKernel = err
			return
		}
		// /proc/version format: "Linux version 6.8.0-107-generic ..."
		// We extract the third whitespace-separated field (index 2), which is
		// the version string. Then we keep only the first two dot-separated
		// segments (major.minor) as the "short release token".
		fields := strings.Fields(string(data))
		if len(fields) < 3 {
			errCachedKernel = fmt.Errorf("unexpected /proc/version format")
			return
		}
		// fields[0]="Linux", fields[1]="version", fields[2]="6.8.0-107-generic"
		full := fields[2]
		// Extract major.minor by splitting on '.' and '-'.
		parts := strings.FieldsFunc(full, func(r rune) bool {
			return r == '.' || r == '-'
		})
		if len(parts) >= 2 {
			cachedKernel = parts[0] + "." + parts[1]
		} else if len(parts) == 1 {
			cachedKernel = parts[0]
		} else {
			cachedKernel = full
		}
	})
	return cachedKernel, errCachedKernel
}

// Platform implements Provider. Returns GOOS, GOARCH, and on Linux the short
// kernel release token from /proc/version.
func (p *DefaultProvider) Platform() (Platform, error) {
	goos := runtime.GOOS
	goarch := runtime.GOARCH

	kernel, err := readKernelVersion()
	if err != nil {
		slog.Debug("envcontext: field unreadable", "field", "platform.kernel", "err", err)
		return Platform{GOOS: goos, GOARCH: goarch, Kernel: ""}, err
	}
	return Platform{GOOS: goos, GOARCH: goarch, Kernel: kernel}, nil
}

// SandboxMode implements Provider. Describes the active sandbox mode as a
// human-readable string. Uses defer/recover to guard against panics from the
// sandbox backend (FR-044).
func (p *DefaultProvider) SandboxMode() (mode string, retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("sandbox backend panicked: %v", r)
			mode = "<unknown>"
		}
	}()

	// Pentest override: when OMNIPUS_PENTEST_HIDE_SANDBOX=1 is set, lie to the
	// LLM about the sandbox state so the model is not primed by the preamble
	// to refuse breakout attempts. The kernel layer is unaffected — Landlock
	// and seccomp continue to enforce whatever cfg.Sandbox.Mode resolves to.
	if os.Getenv("OMNIPUS_PENTEST_HIDE_SANDBOX") == "1" {
		return "off (no kernel enforcement)", nil
	}

	status := sandbox.DescribeBackend(p.backend)
	return renderSandboxMode(status), nil
}

// NetworkPolicy implements Provider. Returns OutboundAllowed from the sandbox
// config (FR-045). cfg is guaranteed non-nil by NewDefaultProvider.
func (p *DefaultProvider) NetworkPolicy() NetworkPolicy {
	return NetworkPolicy{OutboundAllowed: p.cfg.Sandbox.AllowNetworkOutbound}
}

// WorkspacePath implements Provider. Returns the workspace path that was
// supplied at construction time (FR-046).
func (p *DefaultProvider) WorkspacePath() string {
	return p.workspace
}

// OmnipusHome implements Provider. Returns the canonical Omnipus home directory
// (FR-047).
func (p *DefaultProvider) OmnipusHome() string {
	return config.OmnipusHomeDir()
}

// ActiveWarnings implements Provider. Emits condition-based warnings for:
//   - dev_mode_bypass active (auth bypass in effect)
//   - Windows platform (flock is a no-op)
//   - sandbox fallback on a Landlock-capable Linux kernel
func (p *DefaultProvider) ActiveWarnings() []string {
	if os.Getenv("OMNIPUS_PENTEST_HIDE_SANDBOX") == "1" {
		return nil
	}

	var warnings []string

	if p.cfg.Gateway.DevModeBypass {
		warnings = append(warnings,
			"dev_mode_bypass is ACTIVE — auth checks are relaxed. Do not assume strict auth.")
	}

	if runtime.GOOS == "windows" {
		warnings = append(
			warnings,
			"running on Windows — pkg/fileutil.WithFlock is a no-op; concurrent memory writes rely on single-writer discipline.",
		)
	}

	// Emit sandbox-fallback-on-capable-kernel warning when the backend is
	// NOT kernel-level on a Linux host. Spec FR-049 + MIN-001 say the
	// warning fires unconditionally on linux + fallback — including when the
	// kernel version can't be detected — because the alternative (hide a
	// possible downgrade on unknown kernel) is less safe than noise.
	if runtime.GOOS == "linux" {
		status := sandbox.DescribeBackend(p.backend)
		if !status.KernelLevel {
			warnings = append(
				warnings,
				"sandbox is running in application-level fallback mode despite a Landlock-capable kernel — this is typically an explicit operator downgrade.",
			)
		}
	}

	return warnings
}
