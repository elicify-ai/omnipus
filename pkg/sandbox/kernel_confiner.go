// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

// KernelChildConfiner is implemented by a backend that enforces policy in the
// KERNEL by confining each child at spawn time, rather than by restricting the
// current process and relying on inheritance.
//
// This exists so the gateway can decide whether to install a policy on a
// capability rather than on a backend NAME. The previous approach compared
// `backendName == "seatbelt"` against a string constant duplicated in a second
// package: renaming the backend, or adding a second kernel backend (Windows Job
// Objects), would silently drop the gateway into the "degraded" branch and boot
// unconfined with no error — the same silent-downgrade shape that already cost
// this branch one round of debugging.
//
// It is intentionally NOT satisfied by FallbackBackend. Fallback implements
// Apply (it records the policy for application-level path checks) but confines
// nothing at the kernel layer, so it must keep taking the degraded path.
//
// LinuxBackend does not implement it either: Landlock restricts the calling
// thread and children inherit, which is a different model with its own
// mode-aware entry point (ApplyWithMode) and its own seccomp sequencing.
type KernelChildConfiner interface {
	// Apply validates the policy and installs it for subsequent children.
	Apply(policy SandboxPolicy) error

	// ConfinesChildren reports that this backend enforces in the kernel by
	// wrapping each spawned child. A backend may implement the interface and
	// still return false (e.g. probe failed), so callers must check.
	ConfinesChildren() bool

	// PolicyApplied reports whether Apply has successfully installed a policy
	// in this process. Status reporting uses this to distinguish "a kernel
	// backend was selected" from "a policy is actually in force" — reporting
	// the former as the latter is how an unconfined gateway comes to look
	// healthy.
	PolicyApplied() bool
}
