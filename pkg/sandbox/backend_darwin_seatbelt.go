//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// macOS Seatbelt sandbox backend (ADR-052 Phase-3, AC-6).
//
// This backend is ACTIVE: sandbox_darwin.go's selectBackendPlatform prefers it
// over FallbackBackend whenever Available() reports true, and
// applyPlatformHardening (hardened_exec_darwin.go) wraps every hardened-exec
// child with it. It was promoted out of DRAFT once the system preamble was
// derived empirically on macOS and the integration/adversarial tests landed —
// see seatbeltSystemPreamble and seatbelt_integration_darwin_test.go.
//
// # CGo tension (hard constraint #2)
//
// The no-CGo path to Seatbelt is to render a profile and launch the child via
// /usr/bin/sandbox-exec. The C API (sandbox_init(3)) would require CGo, which
// is forbidden. sandbox-exec is a signed Apple binary and is the documented
// viable no-CGo enforcement primitive; invoking it is distinct from the "no
// ldd/os-exec for diagnostics" rule — this is the security enforcement layer.
//
// # Posture difference from Linux — read this before assuming parity
//
// Landlock restricts the CURRENT THREAD and every child inherits the domain,
// so the gateway process confines itself. sandbox-exec can only launch a FRESH
// child inside a profile; there is no no-CGo way to push an already-running
// process into a Seatbelt domain. Consequently THE GATEWAY PROCESS ITSELF IS
// NOT SEATBELT-CONFINED on macOS — only the children it spawns are. A
// compromise of the gateway process is therefore not contained by Seatbelt the
// way it would be by Landlock. This is an accepted, documented limitation of
// the no-CGo constraint, surfaced in the boot log by Apply().

package sandbox

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
)

// seatbeltDisableEnv is an operator kill-switch. When set to "1", Available()
// reports false and darwin falls back to app-level enforcement. It exists so a
// macOS host that hits an unforeseen profile incompatibility can degrade
// gracefully (HC #4) without a rebuild, and so the fallback path stays
// exercisable in the field. It is a DISABLE switch by design: the backend is
// on by default, and no environment variable is needed to get protection.
//
// It replaces the retired OMNIPUS_SANDBELT_ENABLE draft gate, which defaulted
// the backend OFF. That variable is intentionally not honoured any more —
// keeping it would let a stale export silently disable the sandbox.
const seatbeltDisableEnv = "OMNIPUS_SEATBELT_DISABLE"

// seatbeltExecPath is the signed Apple binary used to launch a child inside a
// Seatbelt profile. See the file header for why shelling out to it is the
// sanctioned no-CGo enforcement path.
const seatbeltExecPath = "/usr/bin/sandbox-exec"

// currentSeatbeltBackend holds the backend installed by the most recent
// successful Apply(), so the spawn path (applyPlatformHardening) can reach the
// active policy without threading it through every call site. This mirrors
// CurrentLinuxBackend() on Linux, which serves the same purpose for Landlock.
var currentSeatbeltBackend atomic.Pointer[SeatbeltBackend]

// CurrentSeatbeltBackend returns the Seatbelt backend installed by Apply, or
// nil when no policy has been applied (in which case children are spawned
// unwrapped, exactly as they were before this backend existed).
func CurrentSeatbeltBackend() *SeatbeltBackend {
	return currentSeatbeltBackend.Load()
}

// seatbeltProfileCacheCap bounds the number of distinct rendered profiles
// seatbeltCache holds at once. Spec unified-file-access-and-mounts FR-4.1/
// FR-4.4.
//
// Sizing: the cache is keyed by the AUTHORED policy, which varies per turn
// (per agent, per workspace re-root — see DeriveKernelPolicy), not per
// spawn — many spawns within the same turn share one key and hit the cache
// repeatedly. 32 is sized for "several dozen concurrently active turns",
// which comfortably covers a single-operator gateway's realistic concurrency
// (a handful of agents, occasionally a few delegated sub-turns each) with
// headroom. At the measured worst case of ~150 KB per rendered profile
// (spec FR-4.3's 142 KB plus slack), 32 entries is ~4.8 MB — a fraction of
// the <10 MB total security-feature RAM budget (CLAUDE.md hard constraint
// #3) shared across every backend in this package, not just this cache.
// Bounded rather than unbounded because an unbounded map keyed by policy
// content grows for the life of the process with every distinct turn shape
// the gateway ever sees (every workspace re-root, every mount add/remove
// produces a new key) — that is an unbounded-memory leak dressed up as a
// cache.
const seatbeltProfileCacheCap = 32

// seatbeltCache is the process-wide render cache used by every
// SeatbeltBackend instance (see ApplyToCmd). It is package-level rather than
// a field on SeatbeltBackend because the cache is keyed by the policy's
// CONTENT (seatbeltPolicyCacheKey), not by anything backend-instance-
// specific — two turns with byte-identical grants should render once
// regardless of which SeatbeltBackend value happens to be
// CurrentSeatbeltBackend() at the time, and a per-instance cache would only
// fragment hit rate for no benefit (every production process has exactly one
// backend installed by Apply() anyway; tests that construct several via
// NewSeatbeltBackend still share this one cache, which is correct since the
// same policy content always renders to the same profile).
var seatbeltCache = newSeatbeltProfileCache(seatbeltProfileCacheCap)

// SeatbeltBackend applies a SandboxPolicy on macOS by rendering a Seatbelt
// profile and launching each hardened-exec child under /usr/bin/sandbox-exec.
type SeatbeltBackend struct {
	// mu guards renderedProfile. ApplyToCmd is called from every spawn, which
	// may run concurrently across agent turns, while Apply may re-install a
	// policy on config reload.
	mu sync.RWMutex

	// renderedProfile is the profile produced by the last successful Apply().
	// It is reused by ApplyToCmd so a child inherits the exact policy the
	// gateway validated, without re-rendering on every spawn.
	renderedProfile string
}

// ConfinesChildren reports that Seatbelt enforces by wrapping each spawned
// child. It mirrors Available() so a host where sandbox-exec is missing or the
// operator kill-switch is set does not claim kernel confinement.
func (s *SeatbeltBackend) ConfinesChildren() bool { return s.Available() }

// PolicyApplied reports whether Apply has installed a profile in this process.
//
// Without this, status reporting inferred enforcement from backend SELECTION,
// so a gateway that selected Seatbelt but never applied a policy — the exact
// bug this branch already had once — reported itself as confined.
func (s *SeatbeltBackend) PolicyApplied() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.renderedProfile != ""
}

// Compile-time assertion that SeatbeltBackend is a kernel child confiner, so
// the gateway's capability check cannot silently stop matching it.
var _ KernelChildConfiner = (*SeatbeltBackend)(nil)

// NewSeatbeltBackend creates a SeatbeltBackend. Construction does NOT imply
// activation — callers must still check Available() before relying on it.
func NewSeatbeltBackend() *SeatbeltBackend {
	return &SeatbeltBackend{}
}

func (s *SeatbeltBackend) Name() string { return "seatbelt" }

// Available reports whether the Seatbelt backend can be used on this host.
//
// This is a REAL capability probe, not a feature flag: it verifies that
// /usr/bin/sandbox-exec exists and is executable. If Apple ever removes or
// relocates it, Available() reports false and selectBackendPlatform degrades
// to app-level enforcement rather than failing every spawn (HC #4).
//
// The operator kill-switch (seatbeltDisableEnv) is checked first so a host can
// force the fallback path without a rebuild.
func (s *SeatbeltBackend) Available() bool {
	if os.Getenv(seatbeltDisableEnv) == "1" {
		slog.Warn("seatbelt backend disabled by operator env override; falling back to application-level enforcement",
			"env", seatbeltDisableEnv)
		return false
	}

	fi, err := os.Stat(seatbeltExecPath)
	if err != nil {
		slog.Warn("seatbelt backend unavailable: sandbox-exec not found",
			"path", seatbeltExecPath, "error", err)
		return false
	}
	if fi.IsDir() || fi.Mode().Perm()&0o111 == 0 {
		slog.Warn("seatbelt backend unavailable: sandbox-exec is not an executable file",
			"path", seatbeltExecPath, "mode", fi.Mode().String())
		return false
	}
	return true
}

// Apply validates the policy by rendering it to a Seatbelt profile, caches the
// result for reuse by ApplyToCmd, and installs this backend as the process-wide
// active one.
//
// IMPORTANT — the current process is NOT restricted here; only children are.
// See the file header for why (sandbox-exec cannot confine a running process
// without CGo). The Warn below makes that posture explicit in the boot log
// rather than leaving operators to infer parity with Landlock.
func (s *SeatbeltBackend) Apply(policy SandboxPolicy) error {
	profile, err := renderSeatbeltProfile(policy)
	if err != nil {
		return fmt.Errorf("seatbelt Apply: %w", err)
	}

	s.mu.Lock()
	s.renderedProfile = profile
	s.mu.Unlock()

	currentSeatbeltBackend.Store(s)

	slog.Warn("seatbelt backend active: hardened-exec children are confined, but the gateway process itself is NOT seatbelt-sandboxed (no-CGo limitation; Landlock-style self-confinement is unavailable on macOS)",
		"backend", s.Name(),
		"filesystem_rules", len(policy.FilesystemRules),
		"bind_ports", len(policy.BindPortRules),
		"connect_ports", len(policy.ConnectPortRules),
	)
	return nil
}

// ApplyToCmd rewrites cmd so that starting it executes the original argv under
// /usr/bin/sandbox-exec with the rendered profile.
//
// The profile is passed INLINE via `-p`, not written to a temp file. That is a
// security decision, not a convenience one: a profile file is read by
// sandbox-exec after we write it, so any attacker able to replace that path in
// the window between write and exec would choose the sandbox policy — a total
// bypass of the boundary this code exists to enforce. Passing the profile in
// argv closes that window entirely and leaves no policy artifact on disk.
//
// The tradeoff is that the profile text is visible in the process argv (e.g.
// `ps`). It contains workspace paths and port numbers — no secrets — and those
// same paths already appear in the child's own argv, so this discloses nothing
// new.
//
// Size is not a practical constraint, but it is bigger than it looks: a
// minimal policy renders to ~1 KB, while a realistic gateway policy carrying a
// 1000-port dev-server range (DefaultPolicy + bind/connect rules, one line per
// port) renders to ~95 KB. Both are comfortably under the 1 MB ARG_MAX, and a
// production-scale profile is verified to spawn a real child — but if the port
// range ever grows by an order of magnitude, this is the limit to re-check.
//
// If cmd is nil or has no Path set, an error is returned — there is nothing to
// wrap, and silently starting an unconfined child would be the worst outcome.
func (s *SeatbeltBackend) ApplyToCmd(cmd *exec.Cmd, policy SandboxPolicy) error {
	if cmd == nil {
		return fmt.Errorf("seatbelt ApplyToCmd: nil cmd")
	}
	if cmd.Path == "" {
		return fmt.Errorf("seatbelt ApplyToCmd: cmd.Path is empty (nothing to wrap)")
	}

	// A SUPPLIED policy is always rendered. There is deliberately no
	// "the policy looks empty, use the boot profile instead" branch here.
	//
	// There used to be one, and it was a silent downgrade. It decided "the
	// caller supplied nothing" by inspecting three of SandboxPolicy's six
	// fields (FilesystemRules, BindPortRules, ConnectPortRules), so a policy
	// that genuinely WAS supplied but happened to carry only DeniedPaths, or
	// only ReadsOpen/ExecOpen, was swapped for the wider boot profile with
	// nothing but a Debug log to say so. "Caller supplied no policy" and
	// "caller supplied a policy that turned out to be empty" are different
	// facts with opposite correct handling, and only one of them is knowable
	// from the policy VALUE — which is why the distinction now lives where it
	// is actually available: applyPlatformHardening holds a *SandboxPolicy and
	// can see the nil. It calls ApplyBootProfileToCmd for the nil case and this
	// method otherwise.
	//
	// A supplied policy with no allows at all therefore reaches
	// renderSeatbeltProfile, which refuses it ("would brick any child") and
	// aborts the spawn. That is the fail-closed outcome: a caller who computed
	// a policy and got nothing has a bug, and inheriting the boot profile would
	// hide it behind a child that runs with MORE reach than intended.
	//
	// seatbeltCache is keyed by the policy's semantic content (see
	// seatbeltPolicyCacheKey), not by anything about this backend instance,
	// so it is deliberately package-level and shared across every
	// SeatbeltBackend — two turns with byte-identical grants render once
	// regardless of which backend instance served which spawn.
	profile, err := seatbeltCache.getOrRender(policy)
	if err != nil {
		// FR-4.2 fail-closed: a render/cache-fill failure aborts the
		// spawn. There is no fallback to an unconfined child — matching
		// the Linux RestrictCurrentThread contract ("Returns an error if
		// the kernel rejects the ruleset; callers must abort the spawn
		// rather than fall through to an unrestricted exec.").
		return fmt.Errorf("seatbelt ApplyToCmd: %w", err)
	}
	return s.wrapUnderSandboxExec(cmd, profile)
}

// ApplyBootProfileToCmd wraps cmd in the profile Apply() rendered at boot.
//
// This is the EXPLICIT "no per-turn policy was supplied" path — the fallback
// documented on Limits.KernelPolicy. It is a separate method rather than a
// sentinel value passed to ApplyToCmd so that a caller has to say which of the
// two it means, and so the two cannot be confused by inspecting a policy value
// that cannot distinguish them (see ApplyToCmd's comment).
//
// Errors when Apply has not run, rather than starting an unwrapped child.
func (s *SeatbeltBackend) ApplyBootProfileToCmd(cmd *exec.Cmd) error {
	if cmd == nil {
		return fmt.Errorf("seatbelt ApplyBootProfileToCmd: nil cmd")
	}
	if cmd.Path == "" {
		return fmt.Errorf("seatbelt ApplyBootProfileToCmd: cmd.Path is empty (nothing to wrap)")
	}

	s.mu.RLock()
	profile := s.renderedProfile
	s.mu.RUnlock()
	if profile == "" {
		return fmt.Errorf(
			"seatbelt ApplyBootProfileToCmd: no boot profile available (Apply was never called on this backend)")
	}
	return s.wrapUnderSandboxExec(cmd, profile)
}

// wrapUnderSandboxExec rewrites cmd's argv so starting it runs the original
// command under /usr/bin/sandbox-exec with profile. Shared by both entry points
// so the wrapping — including the double-wrap guard — cannot drift between the
// per-turn and boot paths.
func (s *SeatbeltBackend) wrapUnderSandboxExec(cmd *exec.Cmd, profile string) error {
	// Guard against double-wrapping. Without this, a caller that runs a cmd
	// through ApplyToCmd twice would nest sandbox-exec inside sandbox-exec;
	// the inner profile would apply to the outer sandbox-exec binary itself
	// and the child would fail to start for reasons that look nothing like
	// the actual cause.
	if cmd.Path == seatbeltExecPath {
		return fmt.Errorf("seatbelt ApplyToCmd: cmd is already wrapped in %s (double-wrap)", seatbeltExecPath)
	}

	// Preserve the original argv0 semantics: if the caller set cmd.Args use it
	// verbatim; otherwise exec.Command would have used cmd.Path at Start time,
	// so synthesize [cmd.Path].
	origArgv := cmd.Args
	if len(origArgv) == 0 {
		origArgv = []string{cmd.Path}
	}

	cmd.Args = append(
		[]string{seatbeltExecPath, "-p", profile, "--"},
		origArgv...,
	)
	cmd.Path = seatbeltExecPath

	slog.Debug("seatbelt ApplyToCmd: wrapped child under sandbox-exec",
		"orig_path", origArgv[0],
		"profile_bytes", len(profile),
	)
	return nil
}

// Compile-time assertion that SeatbeltBackend satisfies SandboxBackend.
var _ SandboxBackend = (*SeatbeltBackend)(nil)
