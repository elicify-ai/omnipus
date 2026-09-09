package envcontext

import (
	"fmt"
	"log/slog"
	"os"
	"runtime"
	"strings"
	"sync"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/gateway/middleware"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
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

// PublicURL implements Provider. Delegates to
// middleware.CanonicalGatewayOrigin, the same resolver the CSP/CORS/WS-origin
// fences and web_serve/preview link base use (ADR-044), so the agent's own
// idea of "where can this be reached from outside" always matches what those
// surfaces actually enforce/emit. Returns "" when no origin can be derived
// (e.g. a wildcard bind with no gateway.public_url configured) — the
// renderer omits the preamble line in that case.
func (p *DefaultProvider) PublicURL() string {
	return middleware.CanonicalGatewayOrigin(p.cfg)
}

// ActiveWarnings implements Provider. Emits condition-based warnings for:
//   - dev_mode_bypass active (auth bypass in effect)
//   - Windows platform (flock is a no-op)
//   - sandbox fallback/degradation on a platform that has a kernel backend
//     (Linux Landlock, macOS Seatbelt)
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

	// Emit a sandbox-degradation warning when the SELECTED backend is not
	// kernel-level, on a platform that has a kernel backend available in
	// principle. This must be driven by the backend's actual reported
	// capability (status.KernelLevel), not by runtime.GOOS alone — GOOS only
	// tells us the platform, not whether the platform's kernel backend is
	// actually in play for THIS process.
	//
	// Review finding 2 (MAJOR): before this fix, the check was gated on
	// `runtime.GOOS == "linux"` and DescribeBackend was never even called on
	// darwin. macOS can degrade the exact same way Linux can — SeatbeltBackend
	// .Available() returns false when /usr/bin/sandbox-exec is missing OR when
	// the operator kill-switch OMNIPUS_SEATBELT_DISABLE=1 is set — and in both
	// cases selectBackendPlatform silently falls back to FallbackBackend with
	// zero signal reaching the agent. A stale env var in a shell profile could
	// disable the kernel sandbox with one boot log and nothing after it.
	//
	// Spec FR-049 + MIN-001 say the warning fires unconditionally on
	// linux + fallback — including when the kernel version can't be detected —
	// because the alternative (hide a possible downgrade on unknown kernel) is
	// less safe than noise. The same posture now applies to darwin.
	status := sandbox.DescribeBackend(p.backend)
	if !status.KernelLevel {
		switch runtime.GOOS {
		case "linux":
			warnings = append(
				warnings,
				"sandbox is running in application-level fallback mode despite a Landlock-capable kernel — this is typically an explicit operator downgrade.",
			)
		case "darwin":
			warnings = append(
				warnings,
				"sandbox is running in application-level fallback mode — macOS Seatbelt (sandbox-exec) is unavailable or disabled via OMNIPUS_SEATBELT_DISABLE; spawned children are NOT kernel-confined.",
			)
		case "windows":
			// There is NO Windows sandbox backend at all (unlike linux/darwin,
			// this is never a downgrade from a kernel-capable state — no such
			// state exists on this platform). selectBackendPlatform always
			// returns FallbackBackend on Windows; without this case the agent
			// got zero sandbox warning here despite the project's own docs
			// stating plainly that Windows has no kernel-level enforcement.
			warnings = append(
				warnings,
				"no sandbox backend exists on Windows — this process and any children it spawns run entirely unconfined at the kernel level; filesystem/exec restrictions below are enforced at the application level only, if at all.",
			)
		}
	}

	return warnings
}
