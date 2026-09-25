// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Package gateway — Sprint-J sandbox-apply wiring.
//
// This file implements FR-J-001..016 from docs/internal/plan/sprint-j-sandbox-apply-spec.md.
// It owns the boot-time orchestration of:
//
//   1. Mode resolution (CLI > config > default) with legacy Enabled mapping.
//   2. Backend selection via sandbox.SelectBackend() (Linux vs Fallback).
//   3. Policy computation via sandbox.DefaultPolicy($OMNIPUS_HOME, AllowedPaths).
//   4. Kernel apply: LinuxBackend.ApplyWithMode → SeccompProgram.Install
//      (both gated on LinuxBackend selection — seccomp-alone is never valid).
//   5. Fail-closed on kernel error when mode=enforce on a capable kernel
//      (exit 78 / EX_CONFIG from cmd/omnipus/main.go).
//   6. Status surfacing: /health response carries sandbox.applied, mode, backend.
//   7. Nag banners: permissive-always, production-off (every 60 seconds,
//      no suppression).
//
// Strict invariants:
//   - All sandbox-apply work MUST complete before any HTTP listener binds
//     (FR-J-010, FR-J-016). Boot sequence: unlock → config → selectBackend
//     → Apply → Install → net.Listen. During the Apply→Install→bind window,
//     external TCP probes receive ECONNREFUSED (not HTTP 503).
//   - Seccomp is only installed when the Linux backend is selected
//     (FR-J-014). Fallback backend means no seccomp.
//   - Apply is idempotent per-process (FR-J-009, enforced inside
//     sandbox.LinuxBackend).
//   - No hot-reload of sandbox config (FR-J-015). Config changes require restart.

package gateway

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/elicify-ai/omnipus/pkg/config"
	"github.com/elicify-ai/omnipus/pkg/sandbox"
)

// ExitSandboxConfig is the Sprint-J-specific exit code for Apply/Install
// failure on a capable kernel (sysexits.h EX_CONFIG=78). cmd/omnipus/main.go
// maps sandbox errors to this code so operators and CI pipelines can
// distinguish sandbox-apply failure from a generic boot error (exit 1).
const ExitSandboxConfig = 78

// SandboxApplyOptions carries the inputs for applySandbox. Kept as a struct
// so the boot caller in gateway.go passes one value and new fields can be
// added without churning the signature (e.g. a future test hook).
type SandboxApplyOptions struct {
	// CLIMode is the value parsed from --sandbox. Empty means "no flag".
	// Non-empty CLI value always overrides the config value (CLI > config).
	CLIMode string
	// Cfg is the loaded config. applySandbox reads cfg.Sandbox.Mode and
	// cfg.Sandbox.AllowedPaths.
	Cfg *config.Config
	// HomePath is $OMNIPUS_HOME, the workspace root that gets RWX access.
	HomePath string
	// Backend is the sandbox backend to apply. When nil, applySandbox calls
	// sandbox.SelectBackend() itself. Normally the gateway passes
	// agentLoop.SandboxBackend() so the /api/v1/security/sandbox-status
	// handler observes the Apply-marked state on the same instance.
	//
	// Note: Landlock's restrict_self affects the whole process, so the
	// choice of instance does not affect kernel enforcement — it only
	// affects which instance's PolicyApplied() flag flips to true.
	Backend sandbox.SandboxBackend
	// GetEnv is os.Getenv by default; overridable for tests that need to
	// inject OMNIPUS_ENV without mutating the real env.
	GetEnv func(string) string
	// Stderr is os.Stderr by default; overridable for tests that capture
	// the production / permissive nag banners.
	Stderr *os.File
}

// SandboxApplyResult captures the outcome of applySandbox. The gateway
// retains this so:
//   - the /health handler can surface {sandbox.applied, mode, backend};
//   - the /api/v1/security/sandbox-status handler can enrich the response
//     with mode and disabled_by via sandbox.DescribeBackendWithState;
//   - the nag-banner goroutine knows whether to fire (permissive or
//     production-off) and for what reason.
type SandboxApplyResult struct {
	// Backend is the selected backend (either LinuxBackend or FallbackBackend).
	Backend sandbox.SandboxBackend
	// BackendName is the selected backend's Name() — "landlock-v3",
	// "landlock-v2", "landlock-v1", or "fallback".
	BackendName string
	// Mode is the resolved mode after CLI/config/legacy mapping.
	Mode sandbox.Mode
	// DisabledBy identifies the source of a Mode=Off decision: "cli_flag",
	// "config", or "kernel_unsupported". Empty when Mode is enforce or
	// permissive.
	DisabledBy string
	// ApplyState is the state struct sandbox.DescribeBackendWithState
	// consumes to produce the /api/v1/security/sandbox-status response.
	ApplyState sandbox.ApplyState
	// NagReason is "" when no banner should fire, "permissive" when
	// Mode=Permissive, "production_off" when Mode=Off + OMNIPUS_ENV=production.
	// Used by StartNagBanner to decide whether and what to repeat.
	NagReason string
	// Policy is the SandboxPolicy that was passed to Apply (or that would
	// have been passed if Mode == Off / fallback backend). Retained so the
	// /api/v1/security/sandbox-status endpoint can surface bind/connect
	// port-rule counts to operators without re-deriving them from config.
	// Zero-value SandboxPolicy when Mode == Off or no policy was computed.
	Policy sandbox.SandboxPolicy
}

// resolveMode implements the CLI > config > default precedence rule from
// FR-J-006. Returns (mode, disabledBy, error):
//
//	"cli_flag"  — CLI flag provided (trumps config, even empty-string config)
//	"config"    — Mode came from config (cfgMode non-empty)
//	""          — Mode was derived from defaults (no CLI, empty cfgMode,
//	              and configTouched=false → fresh install)
//
// configTouched indicates whether the operator has written ANY sandbox
// settings to disk (Mode set OR a populated AllowedPaths list). When true
// with an empty Mode we treat it as an explicit "off"; when false we apply
// the "enforce on capable kernels" default for fresh installs.
//
// An invalid CLI value causes an error so cmd/omnipus can exit with code 2
// (usage error) before any boot logic runs (FR-J-006 second sentence).
func resolveMode(
	cliMode, cfgMode string,
	configTouched bool,
	getEnv func(string) string,
) (sandbox.Mode, string, error) {
	// CLI takes priority unconditionally. An empty CLIMode means no flag
	// was passed — defer to config.
	if cliMode != "" {
		mode, err := sandbox.ParseMode(cliMode)
		if err != nil {
			return "", "", err
		}
		return mode, "cli_flag", nil
	}

	// No CLI override. Empty cfgMode + nothing else touched in the sandbox
	// section means a fresh install — apply the "enforce on capable
	// kernels" default. Kernel capability is checked separately by
	// SelectBackend → FallbackBackend on pre-5.13 kernels.
	//
	// Docker compat: when running inside a Docker container, the default
	// unprivileged seccomp profile blocks several syscalls the hardened-exec
	// path needs (RLIMIT_NPROC manipulation, prctl, Landlock prctl). With
	// sandbox=enforce, every exec tool call then fails with "fork/exec
	// /bin/sh: permission denied" and the agent can't do its job.
	// Docker IS the outer isolation layer, so downgrade to permissive: exec
	// works AND operators see what would have been blocked in the audit log.
	// Operators who have configured Docker with the right caps + a custom
	// seccomp can override with OMNIPUS_SANDBOX_MODE=enforce (env tag on
	// cfg.Sandbox.Mode) or --sandbox=enforce.
	if cfgMode == "" && !configTouched {
		if getEnv != nil && isRunningInDocker(getEnv) {
			return sandbox.ModePermissive, "docker_autodetect", nil
		}
		return sandbox.ModeEnforce, "", nil
	}

	mode, err := sandbox.ParseMode(cfgMode)
	if err != nil {
		return "", "", fmt.Errorf("gateway.sandbox.mode: %w", err)
	}
	return mode, "config", nil
}

// dockerenvPath is the filesystem path probed by isRunningInDocker.
// Overridden in tests via a package-level variable so tests can create a
// temp file without requiring root or a real /.dockerenv.
var dockerenvPath = "/.dockerenv"

// isRunningInDocker reports whether the process appears to be inside a
// Docker container. Two signals: OMNIPUS_IN_DOCKER=1 explicit override
// (used by tests), and /.dockerenv presence (the standard runtime marker
// Docker drops into every container).
func isRunningInDocker(getEnv func(string) string) bool {
	if getEnv("OMNIPUS_IN_DOCKER") == "1" {
		return true
	}
	if _, err := os.Stat(dockerenvPath); err == nil {
		return true
	} else if !errors.Is(err, os.ErrNotExist) {
		// EACCES, EPERM, or other non-ENOENT error: the file may exist but is
		// unreadable (e.g. hardened AppArmor profile, read-only root with restricted
		// stat).  Log so operators on those setups know why auto-detect fired or
		// failed, and can set OMNIPUS_IN_DOCKER=1 to force the result.
		slog.Warn("sandbox: /.dockerenv stat failed — defaulting to non-docker mode",
			"err", err,
			"hint", "set OMNIPUS_IN_DOCKER=1 if running inside a container")
	}
	return false
}

// productionNagBanner is the multi-line warning printed to stderr when the
// gateway runs with mode=off in a production environment. Deliberately loud
// and unmissable in journald / Docker logs. FR-J-011: no suppression.
const productionNagBanner = `
======================================================================
WARN: SANDBOX DISABLED IN PRODUCTION ENVIRONMENT
  Omnipus is running with sandbox mode=off while OMNIPUS_ENV=production.
  This is not the deny-by-default posture the security model requires.
  Either set sandbox.mode=enforce or remove OMNIPUS_ENV=production.
  This banner repeats every 60 seconds and cannot be silenced.
======================================================================
`

// permissiveNagBanner is printed when mode=permissive. Permissive mode is
// valid for pre-enforcement audit rollouts but must never ship to production
// without an explicit plan to flip to enforce afterwards.
const permissiveNagBanner = `
======================================================================
WARN: SANDBOX IN PERMISSIVE MODE — NOT ENFORCED. DO NOT USE IN PRODUCTION.
  Policy is computed and audit-logged but violations are NOT blocked.
  Seccomp uses RET_LOG; Landlock restrict_self is skipped on kernels < 6.12.
  Flip sandbox.mode to enforce once you've reviewed the audit log.
======================================================================
`

// applySandboxState carries the shared state of applySandbox across its stages.
type applySandboxState struct {
	opts                   SandboxApplyOptions
	result                 *SandboxApplyResult
	err                    error
	filesystemModel        sandbox.FilesystemModel
	mode                   sandbox.Mode
	disabledBy             string
	backend                sandbox.SandboxBackend
	backendName            string
	linuxBE                linuxApplier
	isLinux                bool
	warnFn                 func(msg string, path string)
	allowedPaths           []string
	bindPorts              []uint16
	abiVersion             int
	kernelConfiner         sandbox.KernelChildConfiner
	confinesChildren       bool
	allowedExecPaths       []string
	policy                 sandbox.SandboxPolicy
	registerTurnPolicyBase func(enforcing bool)
}

// applySandbox is the Sprint-J boot step. It MUST run after credential
// unlock and config load, and MUST complete before any net.Listen call on
// the HTTP port (FR-J-010, FR-J-016).
//
// Returns (result, nil) on success — including the graceful-fallback path
// where the kernel is too old and we selected FallbackBackend.
//
// Returns (result, err) only when the operator asked for enforce/permissive
// on a capable kernel AND the kernel rejected Apply or Install. In that
// case, the caller MUST abort boot (FR-J-004: fail closed, exit code 78,
// never bind the HTTP listener). The returned result is still populated so
// the caller can inspect what was attempted, but the error overrides it.
func applySandbox(opts SandboxApplyOptions) (result *SandboxApplyResult, err error) {
	as := &applySandboxState{opts: opts}

	if result, stop, err := as.resolveFilesystemModel(); stop {
		return result, err
	}
	// Stamped once here rather than at each of the several `return result, nil`
	// sites: a new early return added later would otherwise silently report an
	// empty model, and an empty model reads as "confined" to every consumer.
	defer func() {
		if as.result != nil {
			as.result.ApplyState.FilesystemModel = as.filesystemModel
		}
	}()

	if result, stop, err := as.resolveModeAndBackend(); stop {
		return result, err
	}

	as.buildPolicy()

	as.publishTurnPolicyBase()

	if result, stop, err := as.applyNonLinuxSandbox(); stop {
		return result, err
	}

	return as.applyLinuxSandbox()
}

// resolveFilesystemModel normalizes output streams and validates the configured filesystem model.
func (as *applySandboxState) resolveFilesystemModel() (*SandboxApplyResult, bool, error) {
	if as.opts.GetEnv == nil {
		as.opts.GetEnv = os.Getenv
	}
	if as.opts.Stderr == nil {
		as.opts.Stderr = os.Stderr
	}

	// ADR-062 filesystem model, resolved BEFORE any exit path so the status
	// endpoint reports it even when the sandbox never gets applied (mode=off,
	// a permissive downgrade, an Apply failure). An operator debugging "why can
	// the agent read this" needs to know which model is configured, and the
	// answer is least obvious precisely on the paths that return early.
	//
	// A malformed value ABORTS boot rather than falling back. The value decides
	// whether reads are enumerated or open, and quietly resolving a typo to
	// either hands the operator a posture they did not choose.
	as.filesystemModel = sandbox.FilesystemModelConfined
	if as.opts.Cfg != nil {
		parsed, perr := sandbox.ParseFilesystemModel(
			as.opts.Cfg.Sandbox.FilesystemModel, sandbox.FilesystemModelConfined)
		if perr != nil {
			return nil, true, fmt.Errorf("sandbox config: %w", perr)
		}
		as.filesystemModel = parsed
	}
	return nil, false, nil
}

// resolveModeAndBackend resolves sandbox mode, selects the backend, and handles an explicitly disabled sandbox.
func (as *applySandboxState) resolveModeAndBackend() (*SandboxApplyResult, bool, error) {
	// Step 1 — Resolve mode from CLI + config. CLI > config > default.
	// Validation of the CLI flag string was already done by cobra (see
	// cmd/omnipus/internal/gateway/command.go) with exit code 2, so any
	// error returned here is a bug in the caller; we still guard.
	cfgMode := ""
	configTouched := false
	if as.opts.Cfg != nil {
		cfgMode = string(as.opts.Cfg.Sandbox.Mode)
		// "Operator touched the section" — anything non-zero in the
		// sandbox config tells us not to apply the fresh-install default.
		configTouched = as.opts.Cfg.Sandbox.Mode != "" ||
			len(as.opts.Cfg.Sandbox.AllowedPaths) > 0
	}
	as.mode, as.disabledBy, as.err = resolveMode(as.opts.CLIMode, cfgMode, configTouched, as.opts.GetEnv)
	if as.err != nil {
		return nil, true, as.err
	}

	// Step 2 — Select or reuse backend. SelectBackend never fails; on
	// pre-5.13 kernels or non-Linux it returns FallbackBackend. When the
	// caller provides a backend (normally agentLoop.SandboxBackend()), we
	// reuse it so the status endpoint's PolicyApplied() check observes
	// the Apply-marked state on the same instance.

	if as.opts.Backend != nil {
		as.backend = as.opts.Backend
		as.backendName = as.backend.Name()
	} else {
		as.backend, as.backendName = sandbox.SelectBackend()
	}
	as.result = &SandboxApplyResult{
		Backend:     as.backend,
		BackendName: as.backendName,
		Mode:        as.mode,
	}
	// DisabledBy answers "what turned the sandbox OFF" and its own doc says
	// it is empty for enforce and permissive. resolveMode returns the SOURCE
	// of the mode setting, which is a different question — assigning it here
	// unconditionally made a fully enforcing sandbox report that it had been
	// disabled by config. Measured on a Landlock v7 runner (2026-08-19):
	// backend=landlock-v7, landlock_enforced=true, seccomp_enforced=true,
	// and disabled_by="config" alongside them. Nothing was disabled. The same
	// constant also appeared on a kernel with no Landlock at all, where the
	// real reason (kernel_too_old_or_non_linux) was known and logged — so the
	// field carried no information in either direction.
	if as.mode == sandbox.ModeOff {
		as.result.DisabledBy = as.disabledBy
	}

	// Step 3 — Handle mode=off: no Apply, no Install. Log-only. Arm the
	// production nag if OMNIPUS_ENV=production.
	if as.mode == sandbox.ModeOff {
		as.result.ApplyState = sandbox.ApplyState{
			Mode:       sandbox.ModeOff,
			DisabledBy: orDefault(as.disabledBy, "config"),
		}
		as.result.DisabledBy = orDefault(as.disabledBy, "config")
		slog.Warn("sandbox.disabled",
			"reason", as.result.DisabledBy,
			"mode", "off",
			"backend", as.backendName)
		if strings.EqualFold(as.opts.GetEnv("OMNIPUS_ENV"), "production") {
			fmt.Fprint(as.opts.Stderr, productionNagBanner)
			slog.Warn("sandbox.disabled.nag",
				"reason", "production_environment",
				"banner_repeat_interval_seconds", 60)
			as.result.NagReason = "production_off"
		}
		return as.result, true, nil
	}
	return nil, false, nil
}

// buildPolicy computes the ordered filesystem and network policy and records how secrets are protected.
func (as *applySandboxState) buildPolicy() {
	// Step 4 — Detect Linux kernel capability. If SelectBackend returned
	// FallbackBackend (pre-5.13, non-Linux, Termux, etc.) we cannot apply
	// anything and the sandbox degrades gracefully. FR-J-014 gates seccomp
	// strictly on LinuxBackend selection — no seccomp-alone.
	as.linuxBE, as.isLinux = as.backend.(linuxApplier)

	// NOTE — non-Linux backends are handled AFTER the policy is computed, in
	// the "non-Linux apply" block below. This function used to return right
	// here for every non-Linux backend, which was correct while darwin's only
	// option was FallbackBackend (nothing to apply). Once ADR-052 Phase-3 AC-6
	// gave macOS a real Seatbelt backend, that early return became a silent
	// hole: SelectBackend reported "seatbelt", the boot log named it, and
	// backend.Apply() was NEVER called — so no profile was ever installed and
	// every child ran completely unconfined. Do not restore the early return.

	// Step 5 — Compute the workspace policy. $OMNIPUS_HOME gets RWX;
	// system libs get R; user AllowedPaths gets R or RW with the
	// system-restricted Write strip (FR-J-013). The warnFn closure
	// captures slog so each stripped rule emits a structured WARN.
	as.warnFn = func(msg, path string) {
		slog.Warn(msg, "path", path, "reason", "system_restricted_path")
	}

	if as.opts.Cfg != nil {
		as.allowedPaths = as.opts.Cfg.Sandbox.AllowedPaths
	}

	// Compute the bind-port allow-list for Landlock ABI v4+.
	//
	// Bind ports: every port in cfg.Sandbox.DevServerPortRange. The kernel
	//   does not accept ranges, so we expand to one rule per port. Agents
	//   serving via web_serve (dev mode) and workspace.shell_bg bind here.
	//
	// Connect ports (#155 item 4): DefaultPolicy seeds the policy with
	//   sandbox.DefaultConnectPorts ({53, 80, 443}) so the gateway and every
	//   forked child can reach DNS, HTTP, and HTTPS. We additionally extend
	//   the connect allow-list with DevServerPortRange so children can
	//   connect back to gateway-owned dev servers and the egress proxy
	//   (which binds inside that range when configured). Anything else —
	//   custom backdoor channels, lateral SSH/MySQL/Redis — is denied at the
	//   kernel layer with EACCES.
	//
	// On ABI < 4 we leave bindPorts nil — handledAccessNet stays 0, and
	// passing rules to a kernel that does not handle net access would only
	// trigger the defensive warn-and-skip in ApplyWithMode. Connect rules
	// are still populated by DefaultPolicy but ignored by the kernel; a
	// boot-time WARN documents the degradation.

	as.abiVersion = 0
	if rep, ok := as.backend.(interface{ ABIVersion() int }); ok {
		as.abiVersion = rep.ABIVersion()
	}

	// Port rules are only worth computing when the selected backend actually
	// enforces them. Landlock needs ABI v4+; Seatbelt enforces bind/connect
	// rules unconditionally and exposes no ABI version at all (abiVersion
	// stays 0 on darwin), so gating purely on abiVersion would silently strip
	// every dev-server port rule from the macOS profile.
	as.kernelConfiner, as.confinesChildren = as.backend.(sandbox.KernelChildConfiner)
	enforcePortRules := as.abiVersion >= 4 || (as.confinesChildren && as.kernelConfiner.ConfinesChildren())

	if enforcePortRules && as.opts.Cfg != nil {
		as.bindPorts = sandboxExtraPorts(as.opts.Cfg)
		// NOTE (WebRTC build W1-A / CRIT-001): the managed Chromium used to
		// need a bind-port allow-rule here for its fixed DevTools TCP port
		// (browser.DebugPort, 9223) — removed along with that port. CDP now
		// flows over Chromium's --remote-debugging-pipe (inherited fd 3/4;
		// pkg/tools/browser/cdppipe), which needs no bind() at all, so there
		// is nothing to allow-list for it anymore.
	}
	// allowedExecPaths is read+execute-only (see sandbox.buildExecPathRules).
	// Deliberately NOT folded into configTouched below: it is seeded non-empty
	// on a fresh install, so treating it as "the operator configured something"
	// would make configTouched permanently true and silently disable the
	// Docker permissive auto-downgrade.

	if as.opts.Cfg != nil {
		as.allowedExecPaths = as.opts.Cfg.Sandbox.AllowedExecPaths
	}
	// filesystem_model is seeded non-empty and so, like allowedExecPaths, is
	// deliberately NOT folded into configTouched: treating it as "the operator
	// configured something" would make configTouched permanently true and
	// silently disable the Docker permissive auto-downgrade.
	as.policy = sandbox.DefaultPolicyForModel(
		as.filesystemModel, as.opts.HomePath, as.allowedPaths, as.allowedExecPaths, as.warnFn, as.bindPorts)

	// Spec FR-4.4 / FR-5.3: say once, at boot, how the secret set is actually
	// protected here — the mechanism differs per platform and the difference is
	// invisible at runtime.
	//
	// Review finding 3 (MAJOR): this used to switch on runtime.GOOS ALONE,
	// after backend/backendName (and confinesChildren/kernelConfiner, computed
	// a few lines above for the port-rule gate) were already known — and
	// ignored them. That meant a Linux host pre-5.13, or a container without
	// Landlock, selected FallbackBackend (nothing enforced) yet still logged
	// mechanism=landlock_never_granted; a macOS host with sandbox-exec missing,
	// or mode=permissive (Seatbelt has no audit-only mode — see the non-Linux
	// apply block below), still logged mechanism=seatbelt_deny. The Windows/
	// default branch was honest by contrast (sandbox.secret_set.UNPROTECTED),
	// which made the other two worse: an operator comparing logs reads the
	// difference as meaningful.
	//
	// The fix gates each "protected" branch on the REAL capability the
	// gateway just computed, not the platform name:
	//   - macOS: confinesChildren && kernelConfiner.ConfinesChildren() — the
	//     same capability check enforcePortRules already uses above — AND
	//     mode == enforce, because the non-Linux apply block below proves
	//     Seatbelt installs a profile ONLY under enforce; permissive
	//     deliberately degrades to application-level enforcement (no kernel
	//     profile at all) since Seatbelt has no audit-only mode.
	//   - Linux: isLinux (the backend actually implements linuxApplier, i.e.
	//     SelectBackend chose LinuxBackend because the kernel is capable) AND
	//     mode == enforce, matching the same "permissive doesn't actually
	//     block anything" reasoning — mode == off already returned earlier in
	//     this function, so the only values reaching here are enforce/permissive.
	// Anything else — including a genuinely unsupported platform like
	// Windows — falls through to the UNPROTECTED warning, worded differently
	// depending on whether a kernel backend exists here in principle.
	seatbeltProtects := runtime.GOOS == "darwin" && as.confinesChildren && as.kernelConfiner.ConfinesChildren() && as.mode == sandbox.ModeEnforce
	landlockProtects := as.isLinux && as.mode == sandbox.ModeEnforce
	switch {
	case seatbeltProtects:
		slog.Info("sandbox.secret_set.protected",
			"mechanism", "seatbelt_deny",
			"model", string(as.filesystemModel),
			"mode", string(as.mode),
			"entries", sandbox.SecretEntriesRelative,
			"detail", "children are denied read and write on these paths; the gateway process itself is not Seatbelt-confined")
	case landlockProtects:
		slog.Info("sandbox.secret_set.protected",
			"mechanism", "landlock_never_granted",
			"model", string(as.filesystemModel),
			"mode", string(as.mode),
			"entries", sandbox.SecretEntriesRelative,
			"detail", "Landlock has no deny primitive; children are granted the siblings of these paths and never the paths themselves")
	default:
		detail := "no filesystem sandbox backend exists on this platform; master.key and the credential vault " +
			"are protected by file permissions alone. Do not run untrusted agents here."
		if runtime.GOOS == "darwin" || runtime.GOOS == "linux" {
			detail = fmt.Sprintf(
				"a kernel sandbox backend exists on this platform but is not actively enforcing right now "+
					"(backend=%s, mode=%s); master.key and the credential vault are protected by file permissions "+
					"alone. Do not run untrusted agents here.", as.backendName, as.mode)
		}
		slog.Warn("sandbox.secret_set.UNPROTECTED",
			"platform", runtime.GOOS,
			"backend", as.backendName,
			"mode", string(as.mode),
			"model", string(as.filesystemModel),
			"entries", sandbox.SecretEntriesRelative,
			"detail", detail)
	}

	// Extend the connect-port allow-list (#155 item 4). DefaultPolicy
	// pre-seeds {53, 80, 443}; we append every port in DevServerPortRange so
	// children can dial loopback dev servers and the egress proxy without
	// the kernel intercepting at connect(2), plus every port this gateway
	// itself listens on (see sandboxExtraPorts). Done after DefaultPolicy
	// returns so we don't have to thread an additional parameter through
	// DefaultPolicy's call sites (the redteam test, agent loop, etc.).
	if enforcePortRules && as.opts.Cfg != nil {
		if ports := sandboxExtraPorts(as.opts.Cfg); len(ports) > 0 {
			extra := make([]sandbox.NetPortRule, 0, len(ports))
			for _, p := range ports {
				extra = append(extra, sandbox.NetPortRule{Port: p})
			}
			as.policy.ConnectPortRules = append(as.policy.ConnectPortRules, extra...)
		}
		// NOTE (WebRTC build W1-A / CRIT-001): this used to also allow-list
		// browser.DebugPort (9223) here — the v0.1 fix for "browser.navigate:
		// dial tcp 127.0.0.1:9223: connect: permission denied", since the
		// managed Chromium's DevTools WebSocket bound a fixed loopback TCP
		// port the gateway's chromedp client had to dial. That port is gone:
		// CDP now flows over Chromium's --remote-debugging-pipe (inherited fd
		// 3/4; pkg/tools/browser/cdppipe) entirely within this OS process, so
		// there is no loopback connect(2) to allow-list for it anymore.
	}
	as.result.Policy = as.policy
}

// publishTurnPolicyBase publishes the boot-time inputs used to derive per-turn kernel policies.
func (as *applySandboxState) publishTurnPolicyBase() {
	// Step 5.3 — publish the BOOT HALF of a per-turn kernel policy
	// (ADR-063 D1 / FR-1.3, FR-3.5).
	//
	// sandbox.DeriveKernelPolicy needs the operator's configuration (filesystem
	// model, allowed paths, port ranges) as well as the turn's own authored
	// FSPolicy, and the tool layer that knows the second half has no access to
	// the first. Registering it here — rather than letting pkg/tools recompute
	// it from config on every spawn — keeps DeriveKernelPolicy the single place
	// a kernel policy is ever constructed. A second construction site is
	// exactly how the app layer and the kernel layer drifted apart before.
	//
	// registerTurnPolicyBase is called on EVERY exit path below, with nil
	// wherever no kernel policy ends up in force, so a degraded boot can never
	// leave a base behind that makes spawn sites derive policies nothing will
	// enforce.
	as.registerTurnPolicyBase = func(enforcing bool) {
		if !enforcing {
			sandbox.RegisterTurnPolicyBase(nil)
			return
		}
		sandbox.RegisterTurnPolicyBase(&sandbox.TurnPolicyInput{
			HomePath:         as.opts.HomePath,
			Model:            as.filesystemModel,
			AllowedPaths:     as.allowedPaths,
			AllowedExecPaths: as.allowedExecPaths,
			BindPorts:        as.bindPorts,
			ConnectPorts:     connectPortsFromRules(as.policy.ConnectPortRules),
			WarnFn:           as.warnFn,
		})
	}
	as.registerTurnPolicyBase(false)
}

// applyNonLinuxSandbox applies Seatbelt when available or records the ordered non-Linux degradation path.
func (as *applySandboxState) applyNonLinuxSandbox() (*SandboxApplyResult, bool, error) {
	// Step 5.4 — non-Linux apply. Reached for every backend that is not the
	// LinuxBackend; see the NOTE at the linuxApplier type assertion above for
	// why this is not an early return any more.
	if !as.isLinux {
		if as.confinesChildren && as.kernelConfiner.ConfinesChildren() {
			kernelBackend := as.kernelConfiner
			// PERMISSIVE IS NOT AVAILABLE HERE, AND MUST NOT SILENTLY ENFORCE.
			//
			// Landlock supports an audit-only mode, so on Linux `permissive`
			// computes and logs the policy without restricting anything.
			// Seatbelt has no equivalent: sandbox-exec either applies a
			// profile or it does not. Applying one under `permissive` would
			// give an operator running the documented "watch what would break
			// before turning it on" step full hard enforcement instead —
			// with no banner, no audit_only flag, and no way to tell.
			//
			// So permissive degrades to application-level enforcement and says
			// so, matching what the mode promises rather than what the backend
			// finds convenient.
			if as.mode == sandbox.ModePermissive {
				slog.Warn("sandbox.permissive.unsupported_by_backend",
					"backend", as.backendName,
					"requested_mode", string(as.mode),
					"effect", "kernel profile NOT installed; falling back to application-level enforcement",
					"reason", "seatbelt has no audit-only mode; applying the profile would enforce, which is not what permissive means")
				fmt.Fprint(as.opts.Stderr, permissiveNagBanner)
				as.result.Mode = as.mode
				as.result.NagReason = "permissive"
				as.result.ApplyState = sandbox.ApplyState{
					Mode:      as.mode,
					AuditOnly: true,
					ExtraNotes: []string{
						"macOS Seatbelt has no audit-only mode; permissive does NOT install a kernel profile. " +
							"Use mode=enforce for kernel confinement, or mode=off to disable it explicitly.",
					},
				}
				return as.result, true, nil
			}

			// macOS kernel sandbox (ADR-052 Phase-3 AC-6). Apply installs the
			// rendered profile as the process-wide active policy; every
			// hardened-exec child is then wrapped by applyPlatformHardening.
			if err := kernelBackend.Apply(as.policy); err != nil {
				slog.Error("sandbox.apply_failed",
					"error", err,
					"mode", string(as.mode),
					"backend", as.backendName)
				// Fail closed: Seatbelt has no equivalent of a ruleset the
				// kernel positively rejects as unsupported (Apply either
				// installs a profile or errors on a real problem), so every
				// Apply failure here keeps the same hard-fail Linux applies
				// to everything except sandbox.ErrLandlockRulesetRejected —
				// booting unconfined would silently downgrade the boundary.
				return as.result, true, fmt.Errorf("sandbox: Seatbelt Apply failed: %w", err)
			}
			// The boot profile is installed and every hardened-exec child is
			// now wrapped, so per-turn policies are enforceable from here on.
			as.registerTurnPolicyBase(true)

			as.result.Mode = as.mode
			as.result.ApplyState = sandbox.ApplyState{
				Mode: as.mode,
				ExtraNotes: []string{
					"macOS Seatbelt: hardened-exec children are confined via sandbox-exec; " +
						"the gateway process itself is not (no-CGo limitation, see ADR-052 Phase-3 AC-6)",
				},
			}
			slog.Info("sandbox.applied",
				"backend", as.backendName,
				"mode", string(as.mode),
				"model", string(as.filesystemModel),
				"filesystem_rules", len(as.policy.FilesystemRules),
				"connect_ports", len(as.policy.ConnectPortRules),
				"bind_ports", len(as.policy.BindPortRules))
			return as.result, true, nil
		}

		// Graceful degradation path. Not an error; operator asked for
		// enforce/permissive but the platform cannot provide it. Hard
		// Constraint #4 (CLAUDE.md) requires we continue serving with
		// application-level fallback rather than crashing.
		// Report a reason the operator can act on. "kernel_too_old_or_non_linux"
		// and "kernel does not support Landlock" are actively misleading on
		// macOS, where the usual causes are a missing sandbox-exec or the
		// operator's own OMNIPUS_SEATBELT_DISABLE kill-switch — neither of
		// which has anything to do with kernel age or Landlock.
		degradedReason := "kernel_too_old_or_non_linux"
		degradedNote := "kernel does not support Landlock; falling back to application-level enforcement"
		if runtime.GOOS == "darwin" {
			degradedReason = "seatbelt_unavailable"
			degradedNote = "macOS kernel sandbox (Seatbelt) unavailable — sandbox-exec missing or disabled via " +
				"OMNIPUS_SEATBELT_DISABLE; falling back to application-level enforcement"
		}
		slog.Warn("sandbox.degraded",
			"reason", degradedReason,
			"selected_backend", as.backendName,
			"requested_mode", string(as.mode))
		as.result.Mode = as.mode
		as.result.ApplyState = sandbox.ApplyState{
			Mode:       as.mode,
			ExtraNotes: []string{degradedNote},
		}
		return as.result, true, nil
	}
	return nil, false, nil
}

// applyLinuxSandbox hardens the gateway, applies Landlock before seccomp, and records the final Linux state.
func (as *applySandboxState) applyLinuxSandbox() (*SandboxApplyResult, error) {
	// Step 5.5 — process-level self-hardening (PR_SET_DUMPABLE=0). Closes
	// C6 from the insider-pentest report: same-uid children can read
	// /proc/<gateway-pid>/environ — and therefore OMNIPUS_MASTER_KEY /
	// OMNIPUS_BEARER_TOKEN — without this. Applied BEFORE Landlock so a
	// failure still surfaces via slog. Applied unconditionally because
	// the property is independent of sandbox mode — even ModeOff benefits
	// from /proc hardening.
	if err := sandbox.HardenGatewaySelf(); err != nil {
		slog.Warn("sandbox.harden_gateway_self_failed",
			"error", err,
			"impact", "/proc/<gateway>/environ may be readable by same-uid children")
	}

	// Step 6 — Apply Landlock. Seccomp Install MUST run after this
	// (FR-J-002) because seccomp filters all syscalls including
	// landlock_*; reversing the order would cause Install to block
	// Apply's syscalls.
	//
	// Only a landlock_create_ruleset rejection of the requested rights mask
	// (sandbox.ErrLandlockRulesetRejected — the kernel does not know a bit
	// the ABI-gating table asked for) degrades to FallbackBackend, matching
	// the non-Linux path above (applyNonLinuxSandbox / kernel too old /
	// missing sandbox-exec). Hard Constraint #4 requires graceful
	// degradation on kernels that cannot deliver what the operator asked
	// for; refusing to boot over a missing feature would turn it into a
	// service outage for an operator who picked the default mode on
	// purpose. Every other Apply failure — a net-port rule rejected by a
	// kernel that claims to support it (B1.4-c, below), or a restrict_self
	// failure — is a real misconfiguration on a kernel that should have
	// worked, and keeps the hard-fail: it returns to SandboxBootError so
	// the operator sees it rather than booting with silently incomplete
	// enforcement.
	if err := as.linuxBE.ApplyWithMode(as.policy, as.mode); err != nil {
		if errors.Is(err, sandbox.ErrLandlockRulesetRejected) {
			return as.degradeAfterLandlockFailure(err)
		}
		slog.Error("sandbox.apply_failed",
			"error", err,
			"mode", string(as.mode),
			"backend", as.backendName,
			"abi_version", as.abiVersion)
		return as.result, fmt.Errorf("sandbox: Landlock Apply failed on capable kernel: %w", err)
	}

	// Step 7 — Install seccomp. Permissive mode uses RET_LOG; enforce
	// uses RET_ERRNO(EPERM). Both are gated on Apply having succeeded.
	seccompProg := sandbox.BuildSeccompProgramWithMode(as.mode)
	if err := seccompProg.Install(); err != nil {
		slog.Error("sandbox.install_failed",
			"error", err,
			"mode", string(as.mode),
			"backend", as.backendName)
		return as.result, fmt.Errorf("sandbox: seccomp Install failed on capable kernel: %w", err)
	}

	// Per-turn kernel policy is enforceable only under enforce mode: Landlock's
	// per-thread re-apply (RestrictCurrentThreadWithPolicy) is a no-op when the
	// saved mode is permissive, so registering a base under permissive would
	// promise confinement the backend has explicitly declined to deliver.
	as.registerTurnPolicyBase(as.mode == sandbox.ModeEnforce)

	// Step 8 — Populate result state for /health and /api/.../sandbox-status.
	// abiVersion is already resolved above for the net-rule gating.
	as.result.ApplyState = sandbox.ApplyState{
		Mode:             as.mode,
		LandlockEnforced: as.mode == sandbox.ModeEnforce,
		SeccompEnforced:  as.mode == sandbox.ModeEnforce,
		AuditOnly:        as.mode == sandbox.ModePermissive,
	}

	if as.mode == sandbox.ModePermissive {
		// FR-J-012: prominent banner at boot AND every 60 seconds.
		fmt.Fprint(as.opts.Stderr, permissiveNagBanner)
		// Include disabled_by so operators can distinguish "I set permissive explicitly"
		// from "docker_autodetect downgraded me" without having to curl /health.
		// Mirrors the pattern in the sandbox.disabled log above (architect Finding #5).
		warnArgs := []any{
			"backend", as.backendName,
			"mode", "permissive",
			"landlock_abi", as.abiVersion,
			"seccomp_syscalls", len(seccompProg.BlockedSyscalls()),
			"landlock_enforced", false,
			"seccomp_enforced", false,
			"audit_only", true,
		}
		if as.disabledBy != "" {
			warnArgs = append(warnArgs, "disabled_by", as.disabledBy)
		}
		slog.Warn("sandbox.permissive", warnArgs...)
		as.result.NagReason = "permissive"
	} else {
		slog.Info("sandbox.applied",
			"backend", as.backendName,
			"mode", "enforce",
			"model", string(as.filesystemModel),
			"landlock_abi", as.abiVersion,
			"seccomp_syscalls", len(seccompProg.BlockedSyscalls()))
	}

	return as.result, nil
}

// degradeAfterLandlockFailure swaps the selected backend to FallbackBackend
// after LinuxBackend.ApplyWithMode returned an error wrapping
// sandbox.ErrLandlockRulesetRejected — a kernel that advertised Landlock but
// rejected the requested rights mask on landlock_create_ruleset. The
// contract matches applyNonLinuxSandbox / FallbackBackend: WARN the
// operator, keep serving at application-level enforcement, do NOT escalate
// to SandboxBootError (exit 78). Hard Constraint #4 (CLAUDE.md) — Linux
// 5.13+ features must degrade gracefully to app-level enforcement on older
// kernels — and the parallel non-LinuxSandbox path above are the reasons
// this exists.
//
// Scope: exactly the ruleset-creation rejection, identified via errors.Is
// at the call site — not a net-port rule rejection (B1.4-c) or a
// restrict_self failure, both of which indicate a capable kernel behaving
// inconsistently rather than a genuinely unsupported feature, and both of
// which keep returning to SandboxBootError.
//
// Side effects: replaces as.backend / as.backendName with FallbackBackend,
// clears the linuxApplier handle and the kernelConfiner handle (so any
// later code that asserts on as.isLinux sees the same shape as the
// non-Linux boot path), drops as.abiVersion to 0, and re-registers the
// per-turn policy base as non-enforcing so spawn sites do not derive a
// kernel policy nothing will enforce.
func (as *applySandboxState) degradeAfterLandlockFailure(applyErr error) (*SandboxApplyResult, error) {
	// Capture the originals so the WARN log names what we tried.
	originalBackend := as.backendName
	originalABI := as.abiVersion

	// FallbackBackend is the application-level backend on every platform
	// (sandbox.go::FallbackBackend). Recreating it here is cheap (no
	// syscall, no state) and keeps the result consistent with the
	// non-Linux boot path, which also returns a fresh FallbackBackend
	// instance from selectBackendPlatform.
	fallback := sandbox.NewFallbackBackend()
	if fbErr := fallback.Apply(as.policy); fbErr != nil {
		// The FallbackBackend only fails Apply on path canonicalization
		// errors (sandbox.go:842-844) — those would also have failed the
		// LinuxBackend's path-rule build. Surface them here so a corrupt
		// $OMNIPUS_HOME doesn't silently boot with neither kernel nor
		// app-level rules in place.
		slog.Error("sandbox.degraded.fallback_apply_failed",
			"error", fbErr,
			"requested_backend", originalBackend,
			"requested_abi", originalABI,
			"requested_mode", string(as.mode),
			"original_apply_error", applyErr)
		// Both causes are wrapped (%w twice, Go 1.20+ multi-error wrapping):
		// this failure is a boot abort, and a caller matching on either the
		// Landlock rejection sentinel or the canonicalization error under the
		// FallbackBackend failure must be able to find it with errors.Is.
		// Formatting the second one with %v discarded the fallback cause's
		// identity and left only its text.
		return as.result, fmt.Errorf("sandbox: Landlock rejected ruleset (%w) and FallbackBackend.Apply failed: %w", applyErr, fbErr)
	}

	as.backend = fallback
	as.backendName = fallback.Name()
	as.linuxBE = nil
	as.isLinux = false
	as.kernelConfiner = nil
	as.confinesChildren = false
	as.abiVersion = 0

	slog.Warn("sandbox.degraded",
		"reason", "kernel_rejected_ruleset",
		"requested_backend", originalBackend,
		"requested_abi", originalABI,
		"requested_mode", string(as.mode),
		"fallback_backend", as.backendName,
		"error", applyErr)

	// Spawn sites use the registered base to derive kernel policies at
	// fork time. After this degrade, no kernel policy will be enforced, so
	// unregister the base — passing `false` here makes
	// RegisterTurnPolicyBase(nil) run, which is exactly the "nothing will
	// enforce" state DeriveKernelPolicy is documented to handle (a nil
	// registered base means no overlay, and spawn inherits nothing).
	as.registerTurnPolicyBase(false)

	as.result.Backend = as.backend
	as.result.BackendName = as.backendName
	as.result.Mode = as.mode
	as.result.ApplyState = sandbox.ApplyState{
		Mode: as.mode,
		ExtraNotes: []string{
			fmt.Sprintf(
				"kernel rejected the Landlock ruleset on %s (ABI v%d reported, %s); "+
					"falling back to application-level enforcement. No kernel confinement is active "+
					"for this boot — secret set, port allow-list, and per-turn confinement are NOT "+
					"enforced at the kernel layer. See the sandbox.degraded WARN above for the "+
					"kernel-side error.",
				originalBackend, originalABI, string(as.mode)),
		},
	}
	return as.result, nil
}

// linuxApplier is the internal narrow interface that applySandbox uses to
// call ApplyWithMode on the LinuxBackend without import-cycling via a type
// assertion on *sandbox.LinuxBackend. FallbackBackend does not implement it,
// which is exactly how FR-J-014 (seccomp gated on Linux) is enforced: the
// type assertion fails for Fallback, and the function returns before seccomp
// Install is reached.
type linuxApplier interface {
	ApplyWithMode(policy sandbox.SandboxPolicy, mode sandbox.Mode) error
}

// sandboxExtraPorts returns every TCP port that must be added to BOTH Landlock
// allow-lists (NET_BIND_TCP and NET_CONNECT_TCP) on top of
// sandbox.DefaultConnectPorts ({53, 80, 443}).
//
// Three groups, and they are deliberately the SAME list for bind and connect:
//
//   - cfg.Sandbox.DevServerPortRange — agents bind dev servers here via
//     web_serve / background bash, and other children dial them back.
//
//   - The gateway's OWN listeners: cfg.Gateway.Port, and
//     cfg.Tools.Browser.WebRTCMediaTCPPort when ICE-TCP is configured.
//
//   - The configured platform sign-in issuer's port (CONNECT-only in
//     practice — the gateway is a client here, never a listener — but
//     added to both lists like everything else this function returns,
//     since bindPorts and this function share one call site). See
//     platformAuthIssuerPort's doc comment for why the issuer's port alone
//     covers discovery, authorize AND token.
//
// # Why the gateway's own port belongs here (regression guard)
//
// It was missing, and that is the whole of CI's intermittent
// `net::ERR_NETWORK_ACCESS_DENIED` on the WebRTC live-video test. The gateway
// serves its own start page, its `/preview/…` URLs, and the capture encoder's
// `ws://127.0.0.1:<gateway.port>/api/v1/browser/capture-ingest` socket on
// cfg.Gateway.Port. The managed Chrome is a CHILD of the gateway, so when it
// inherits the Landlock domain its connect(2) to that port returns EACCES,
// which Chromium's MapConnectError turns verbatim into
// ERR_NETWORK_ACCESS_DENIED (net/socket/socket_posix.cc). Every default port
// was outside both allow-lists — production's 5000 as much as CI's 6060 — so
// this was a product defect on any Linux host with Landlock ABI v4+ (kernel
// 6.7+) in enforce mode, not a CI-only artifact.
//
// The BIND half matters for a second, latent reason: the gateway binds its own
// listener AFTER applySandbox runs (pkg/channels/manager.go's StartAll), so
// cfg.Gateway.Port must be bind-allow-listed or the gateway cannot open the
// socket it exists to serve. That it works today at all is an accident of
// Landlock's per-thread semantics (landlock_restrict_self covers only the
// calling thread and threads forked from it), which happens to leave most Go
// worker threads unrestricted — see LinuxBackend.ApplyToCmd's "Landlock
// child-process contract". Do not rely on that accident.
//
// Deliberately NOT widened further: the point of this allow-list is that
// lateral SSH/MySQL/Redis and custom backdoor ports stay denied in the kernel.
// Only ports this process itself listens on are added.
func sandboxExtraPorts(cfg *config.Config) []uint16 {
	if cfg == nil {
		return nil
	}
	seen := make(map[uint16]struct{})
	var out []uint16
	add := func(p int) {
		if p < 1 || p > 65535 {
			return
		}
		port := uint16(p)
		if _, dup := seen[port]; dup {
			return
		}
		seen[port] = struct{}{}
		out = append(out, port)
	}

	if pr := cfg.Sandbox.DevServerPortRange; !pr.IsZero() {
		for p := pr.Min(); p <= pr.Max(); p++ {
			add(int(p))
		}
	}
	add(cfg.Gateway.Port)
	add(cfg.Tools.Browser.WebRTCMediaTCPPort)
	add(platformAuthIssuerPort(cfg))
	return out
}

// platformAuthIssuerPort returns the one extra TCP port sandboxExtraPorts must
// allow-list for the configured platform sign-in issuer (cfg.Security.PlatformAuth.Issuer),
// or 0 when there is nothing to add.
//
// Why the issuer's port alone is enough: editions/platform's discovery flow
// (discovery.go::discoverPlatformEndpoints) fetches RFC 8414 metadata from the
// issuer's own origin, and sameOriginAsIssuer refuses any authorize or token
// endpoint that is not on that EXACT scheme+host — and a URL's host includes
// its port. So the issuer's origin is also the origin every subsequent
// discovery/authorize/token request dials; one allow-listed port covers all
// three. There is no separate JWKS/trust-anchor URL to add alongside it:
// PlatformAuthConfig.Keys is a static, boot-time key list, never fetched (see
// that field's doc comment in pkg/config/platform_auth.go) — a decision this
// function does not need to know about beyond "there is nothing else to add".
//
// Returns 0 for: no issuer configured (the ordinary local-mode instance);
// an issuer that is not an absolute URL (the platform provider already
// refuses a malformed issuer loudly elsewhere — this is a sandbox allow-list,
// not a second validator); or an issuer on the scheme's default port (http
// :80, https :443 — both already in sandbox.DefaultConnectPorts, so adding
// them again would be a harmless but pointless duplicate rule).
func platformAuthIssuerPort(cfg *config.Config) int {
	issuer := strings.TrimSpace(cfg.Security.PlatformAuth.Issuer)
	if issuer == "" {
		return 0
	}
	u, err := url.Parse(issuer)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return 0
	}
	portStr := u.Port()
	if portStr == "" {
		// No explicit port: the scheme's default is already allow-listed.
		return 0
	}
	port, err := strconv.Atoi(portStr)
	if err != nil {
		return 0
	}
	return port
}

// connectPortsFromRules flattens the boot policy's outbound port rules back to
// the plain port list sandbox.TurnPolicyInput takes.
//
// It reads the FINAL rules rather than recomputing them from
// cfg.Sandbox.DevServerPortRange, because the boot policy's connect set is
// assembled in two steps (DefaultPolicyForModel seeds 53/80/443, then the block
// above appends the dev-server range). Recomputing only the second step is how a
// per-turn policy would end up silently missing the first, so the boot policy
// itself is the source of truth.
func connectPortsFromRules(rules []sandbox.NetPortRule) []uint16 {
	if len(rules) == 0 {
		return nil
	}
	out := make([]uint16, 0, len(rules))
	for _, r := range rules {
		out = append(out, r.Port)
	}
	return out
}

// orDefault returns value if non-empty, otherwise fallback.
func orDefault(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// sandboxHealthSetter is the narrow interface the health server satisfies
// for Sprint-J wiring. Extracted from an inline interface type so the
// linter's inamedparam rule is satisfied and the wiring stays testable.
type sandboxHealthSetter interface {
	SetSandboxInfoFunc(fn func() map[string]any)
}

// registerSandboxHealthCheck wires the SandboxApplyResult into the health
// server so GET /health responses include a "sandbox" sub-object with
// {applied, mode, backend}. Sprint-J FR-J-008 requires this field to
// reflect the post-Apply state (true on enforce, false on off/fallback).
// Operators can curl /health | jq .sandbox to verify the runtime state
// without hitting the authenticated /api/v1/security/sandbox-status endpoint.
func registerSandboxHealthCheck(srv sandboxHealthSetter, result *SandboxApplyResult) {
	if srv == nil || result == nil {
		return
	}
	// Build the info map once — FR-J-015 forbids hot-reload of sandbox
	// config, so the values never change after boot. The closure captures
	// the pre-built map to avoid re-allocating on every /health request
	// (this endpoint is hit frequently by k8s readiness probes).
	//
	// "applied" requires BackendName != "fallback" in addition to the mode
	// check: FR-J-008's own contract above is "false on off/fallback", but
	// Mode alone cannot tell fallback apart from real kernel enforcement.
	// Mode is the OPERATOR'S request and survives every degrade path
	// unchanged (applyNonLinuxSandbox's kernel-too-old/seatbelt-unavailable
	// branch and degradeAfterLandlockFailure's ruleset-rejection branch both
	// keep result.Mode = enforce/permissive while swapping BackendName to
	// "fallback"). Gating on Mode alone would report "applied": true for a
	// process that is running application-level enforcement only.
	info := map[string]any{
		"applied": (result.Mode == sandbox.ModeEnforce || result.Mode == sandbox.ModePermissive) &&
			result.BackendName != "fallback",
		"mode":    string(result.Mode),
		"backend": result.BackendName,
	}
	if result.DisabledBy != "" {
		info["disabled_by"] = result.DisabledBy
	}
	if result.ApplyState.AuditOnly {
		info["audit_only"] = true
	}
	if result.ApplyState.LandlockEnforced {
		info["landlock_enforced"] = true
	}
	if result.ApplyState.SeccompEnforced {
		info["seccomp_enforced"] = true
	}
	srv.SetSandboxInfoFunc(func() map[string]any { return info })
}

// StartNagBanner starts a background goroutine that repeats the permissive
// or production-off banner to stderr every 60 seconds (FR-J-011, FR-J-012).
// Returns a cancel function the gateway shutdown path must call to stop the
// goroutine cleanly.
//
// If reason is "", no goroutine is started and a no-op cancel is returned.
func StartNagBanner(reason string, stderr *os.File) context.CancelFunc {
	if reason == "" {
		return func() {}
	}
	if stderr == nil {
		stderr = os.Stderr
	}

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(60 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				switch reason {
				case "permissive":
					fmt.Fprint(stderr, permissiveNagBanner)
					slog.Warn("sandbox.permissive.nag", "banner_repeat_interval_seconds", 60)
				case "production_off":
					fmt.Fprint(stderr, productionNagBanner)
					slog.Warn("sandbox.disabled.nag", "banner_repeat_interval_seconds", 60)
				}
			}
		}
	}()
	return func() {
		cancel()
		wg.Wait()
	}
}
