// Omnipus - Ultra-lightweight personal AI agent
// License: MIT
// Copyright (c) 2026 Omnipus contributors

package sandbox

import (
	"fmt"
	"strings"
	"sync/atomic"

	"github.com/elicify-ai/omnipus/pkg/fspolicy"
)

// This file is the seam that makes DeriveKernelPolicy reachable from a spawn
// site. ADR-061 D1 / spec FR-1.3, FR-3.5.
//
// # The problem it solves
//
// DeriveKernelPolicy needs two halves: the turn's authored fspolicy.FSPolicy
// (which only the tool layer has) and the operator's boot configuration —
// filesystem model, allowed_paths, allowed_exec_paths, the dev-server port
// range (which only the gateway has). pkg/tools cannot read the second half
// without recomputing it from config on every spawn, and recomputing it is how
// the app layer and the kernel layer drifted apart in the first place: a second
// construction site producing a second answer.
//
// So the gateway registers ITS half here, once, at the moment it successfully
// applies the boot policy, and the spawn sites supply the per-turn half.
// DeriveKernelPolicy stays the single construction site for both.
//
// # Why nil is not an error
//
// Not every spawn happens inside an agent turn (MCP server startup, tooling
// invoked before any policy is applied, a gateway running with sandbox mode
// off). Those callers get a nil policy and fall back to the boot-global
// profile, which is exactly the behaviour they had before this seam existed.
// nil means "no per-turn policy"; it has never meant "unconfined".

// turnPolicyBase is the boot-registered half of a per-turn kernel policy, or
// nil when no policy is in force (sandbox off, or a degraded/app-level
// backend). Read on every spawn, written once at boot, so an atomic pointer is
// both correct and free on the hot path.
var turnPolicyBase atomic.Pointer[TurnPolicyInput]

// RegisterTurnPolicyBase installs the boot half of the per-turn kernel policy.
// The gateway calls this immediately after a successful, ENFORCING Apply, and
// calls it with nil whenever no kernel policy is in force.
//
// Passing nil is a deliberate part of the contract rather than an omission: a
// gateway that degrades to application-level enforcement must not leave a stale
// base registered from a previous configuration, or spawn sites would keep
// deriving kernel policies that no backend is going to enforce — the sort of
// discrepancy that makes a security control look present in the logs while
// being absent in the kernel.
//
// The value is copied defensively; the caller may reuse its slices.
func RegisterTurnPolicyBase(in *TurnPolicyInput) {
	if in == nil {
		turnPolicyBase.Store(nil)
		return
	}
	cp := *in
	cp.AllowedPaths = append([]string(nil), in.AllowedPaths...)
	cp.AllowedExecPaths = append([]string(nil), in.AllowedExecPaths...)
	cp.BindPorts = append([]uint16(nil), in.BindPorts...)
	cp.ConnectPorts = append([]uint16(nil), in.ConnectPorts...)
	turnPolicyBase.Store(&cp)
}

// TurnPolicyBaseInstalled reports whether a boot base is registered, i.e.
// whether KernelPolicyForTurn can produce anything at all. Exported for status
// reporting and tests; the spawn path does not need it, since a missing base
// simply yields a nil policy.
func TurnPolicyBaseInstalled() bool { return turnPolicyBase.Load() != nil }

// KernelPolicyForTurn derives the PER-TURN kernel policy for authored, ready to
// be assigned to Limits.KernelPolicy.
//
// Returns (nil, nil) when no boot base is registered — the documented "no
// per-turn policy, use whatever the boot profile is" fallback. Every spawn site
// may assign the result unconditionally.
//
// Returns an error when a base IS registered but authored cannot produce a
// meaningful policy. That case fails CLOSED at the call site rather than
// silently falling back to the boot profile, because the boot profile is the
// WIDER of the two: falling back on a malformed turn policy would hand the
// child more reach than the turn was entitled to, which is the precise failure
// this whole wiring exists to remove.
func KernelPolicyForTurn(authored fspolicy.FSPolicy) (*SandboxPolicy, error) {
	base := turnPolicyBase.Load()
	if base == nil {
		return nil, nil
	}
	if strings.TrimSpace(authored.WorkDir) == "" {
		return nil, fmt.Errorf(
			"sandbox: cannot derive a per-turn kernel policy from an FSPolicy with no WorkDir " +
				"(the work dir IS the turn's write grant); refusing to fall back to the wider boot profile")
	}
	policy := DeriveKernelPolicy(authored, *base)
	return &policy, nil
}
