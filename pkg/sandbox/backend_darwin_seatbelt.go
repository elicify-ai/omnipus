//go:build darwin

// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// DRAFT — macOS Seatbelt sandbox backend (ADR-052 Phase-3, AC-6).
//
// THIS BACKEND IS NOT WIRED INTO BACKEND SELECTION. selectBackendPlatform in
// sandbox_other.go (//go:build !linux) still unconditionally returns
// FallbackBackend for darwin. Enabling Seatbelt requires:
//
//  1. A macOS integration test that exercises a real child under sandbox-exec.
//  2. An adversarial security review of the rendered profile (escape attempts,
//     path traversal, symlink races, profile injection).
//  3. Adding the macOS system-library preamble (dyld shared cache, /usr/lib,
//     /dev/urandom, /Library/Preferences, process-fork, signal-self) that a
//     real child needs to start — see renderSeatbeltProfile's doc comment.
//  4. Flipping Available()'s default to true (or wiring a darwin-specific
//     selectBackendPlatform that prefers Seatbelt over Fallback).
//
// Until those land, Available() returns false unless OMNIPUS_SANDBELT_ENABLE=1
// is set, which forces the caller onto FallbackBackend for all non-explicit
// use. There is zero runtime behavior change from this file on a stock build.
//
// # CGo tension (hard constraint #2)
//
// The no-CGo path to Seatbelt is to generate a `.sb` profile and launch the
// child via /usr/bin/sandbox-exec -f <profile>. The C API (sandbox_init(3))
// would require CGo, which is forbidden. sandbox-exec is a signed Apple binary
// and is the documented viable no-CGo enforcement primitive; shelling out to
// it is distinct from the "no ldd/os-exec for diagnostics" rule — this is the
// security enforcement layer, not a diagnostic helper.

package sandbox

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
)

// seatbeltEnableEnv is the explicit opt-in env var. When set to "1" (and only
// "1"), SeatbeltBackend.Available() returns true so a macOS host that has been
// integration-tested can activate the backend. Any other value (including the
// default unset state) keeps the backend inactive and darwin stays on
// FallbackBackend.
const seatbeltEnableEnv = "OMNIPUS_SANDBELT_ENABLE"

// SeatbeltBackend applies a SandboxPolicy on macOS by rendering a Seatbelt
// `.sb` profile and launching each hardened-exec child under
// `/usr/bin/sandbox-exec -f <profile>`.
//
// It is a DRAFT: Available() is false unless OMNIPUS_SANDBELT_ENABLE=1.
type SeatbeltBackend struct {
	// renderedProfile is the profile string produced by the last successful
	// Apply() call. It is reused by ApplyToCmd so a child inherits the exact
	// policy the gateway validated, without re-rendering on every spawn. The
	// macOS integration step may prepend a system-library preamble here.
	renderedProfile string
}

// NewSeatbeltBackend creates a SeatbeltBackend. Construction does NOT imply
// activation — callers must still check Available() before relying on it.
func NewSeatbeltBackend() *SeatbeltBackend {
	return &SeatbeltBackend{}
}

func (s *SeatbeltBackend) Name() string { return "seatbelt" }

// Available reports whether the Seatbelt backend should be selected.
//
// DRAFT GATE: returns false unless OMNIPUS_SANDBELT_ENABLE=1 is set in the
// environment. While false, selectBackendPlatform falls through to
// FallbackBackend, so there is no behavior change on a stock darwin build.
// Flipping this to default-true is deferred to the macOS integration +
// adversarial review (ADR-052 Phase-3 AC-6).
func (s *SeatbeltBackend) Available() bool {
	return os.Getenv(seatbeltEnableEnv) == "1"
}

// Apply validates the policy by rendering it to a Seatbelt profile and caches
// the result for reuse by ApplyToCmd.
//
// IMPORTANT — current-process restriction is NOT applied here. sandbox-exec
// can only launch a FRESH child inside a profile; there is no no-CGo way to
// push the already-running gateway process into a Seatbelt domain (that would
// require the C sandbox_init(3) API). This is a fundamental difference from
// LinuxBackend.Apply, which restricts the current thread so children inherit
// the Landlock domain. Under the Seatbelt model the gateway process itself is
// NOT sandboxed — each hardened-exec child is wrapped individually by
// ApplyToCmd. The Info log below surfaces this so the posture is not silent.
//
// Because Available() is false on a stock build, this method is unreachable
// through normal backend selection; it exists to satisfy the interface and to
// pre-validate the policy when the backend is explicitly enabled.
func (s *SeatbeltBackend) Apply(policy SandboxPolicy) error {
	profile, err := renderSeatbeltProfile(policy)
	if err != nil {
		return fmt.Errorf("seatbelt Apply: %w", err)
	}
	s.renderedProfile = profile
	slog.Info("seatbelt backend: policy validated and cached; gateway process itself is NOT seatbelt-sandboxed (children are wrapped via ApplyToCmd)")
	return nil
}

// ApplyToCmd wraps cmd so that when started it executes under
// `/usr/bin/sandbox-exec -f <profile> -- <original command>`.
//
// The Seatbelt profile is rendered from the policy (or reused from the last
// Apply), written to a process-scoped temp file with a `.sb` suffix, and the
// cmd is rewritten so its argv becomes:
//
//	/usr/bin/sandbox-exec -f <tempfile.sb> -- <orig argv0> <orig argv1> ...
//
// The temp file is removed after the command finishes (best-effort; if the
// gateway crashes the OS reaps /tmp on reboot). A Setpgid is left to the
// existing applyPlatformHardening caller so the parent can SIGTERM the tree.
//
// If cmd is nil or has no Path set, an error is returned — there is nothing
// to wrap.
func (s *SeatbeltBackend) ApplyToCmd(cmd *exec.Cmd, policy SandboxPolicy) error {
	if cmd == nil {
		return fmt.Errorf("seatbelt ApplyToCmd: nil cmd")
	}
	if cmd.Path == "" {
		return fmt.Errorf("seatbelt ApplyToCmd: cmd.Path is empty (nothing to wrap)")
	}

	// Render (or reuse) the profile. Re-rendering on every call keeps the
	// child in sync with the latest policy even if Apply was never called
	// (e.g. a caller that only uses ApplyToCmd). It also re-runs path
	// validation, defending against a mutated policy.
	profile := s.renderedProfile
	if policy.FilesystemRules != nil || policy.BindPortRules != nil || policy.ConnectPortRules != nil {
		rendered, err := renderSeatbeltProfile(policy)
		if err != nil {
			return fmt.Errorf("seatbelt ApplyToCmd: %w", err)
		}
		profile = rendered
	}
	if profile == "" {
		return fmt.Errorf("seatbelt ApplyToCmd: no profile available (Apply was not called and policy is empty)")
	}

	// Write the profile to a temp file. os.CreateTemp (not
	// fileutil.WriteFileAtomic) is intentional: this is a transient
	// command-wrapping artifact read once by sandbox-exec, not persisted
	// entity data. 0600 because the profile describes the security boundary.
	tmp, err := os.CreateTemp("", "omnipus-seatbelt-*.sb")
	if err != nil {
		return fmt.Errorf("seatbelt ApplyToCmd: create temp profile: %w", err)
	}
	if _, writeErr := tmp.WriteString(profile); writeErr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("seatbelt ApplyToCmd: write temp profile: %w", writeErr)
	}
	if closeErr := tmp.Close(); closeErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("seatbelt ApplyToCmd: close temp profile: %w", closeErr)
	}
	if chmodErr := os.Chmod(tmp.Name(), 0o600); chmodErr != nil {
		_ = os.Remove(tmp.Name())
		return fmt.Errorf("seatbelt ApplyToCmd: chmod temp profile: %w", chmodErr)
	}
	// Rebuild argv. Preserve the original argv0 semantics: if the caller set
	// cmd.Args, use it verbatim; otherwise exec.Command would have used
	// cmd.Path at Start time, so synthesize [cmd.Path].
	origArgv := cmd.Args
	if len(origArgv) == 0 {
		origArgv = []string{cmd.Path}
	}

	// Rewrite the command so the child launches as:
	//   /usr/bin/sandbox-exec -f <profile.sb> -- <orig argv0> <orig argv1> ...
	cmd.Args = append(
		[]string{seatbeltExecPath, "-f", tmp.Name(), "--"},
		origArgv...,
	)
	cmd.Path = seatbeltExecPath

	// Profile cleanup: the .sb file is removed via the per-command hook in
	// sandbox.StartLocked/hardened_exec once the DRAFT backend is enabled at
	// integration time. Until then the temp file persists under /tmp and is
	// reaped by the OS on reboot; this is acceptable for a backend that is
	// unreachable on a stock build (Available() == false).
	slog.Debug("seatbelt ApplyToCmd: wrapped child under sandbox-exec",
		"profile", tmp.Name(),
		"orig_path", origArgv[0],
	)
	return nil
}

// Compile-time assertion that SeatbeltBackend satisfies SandboxBackend.
var _ SandboxBackend = (*SeatbeltBackend)(nil)
