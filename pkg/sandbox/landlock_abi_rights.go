// Omnipus - Ultra-lightweight personal AI gateway
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Platform-neutral Landlock ABI-rights table.
//
// Everything in this file is compiled on every supported OS so the
// ABI-gating logic can be unit-tested without a Linux kernel. The actual
// syscall plumbing lives in sandbox_linux.go (linux build tag); this file
// holds the constants, the landlockRulesetAttr struct shape, and the
// single source of truth for the per-ABI rights mask (landlockRightsForABI).
//
// Both writers (sandbox_linux.go::computeRights and the Status endpoint via
// DetectLandlockABI) read from landlockRightsForABI / these gates so the
// ruleset attr declared on create_ruleset and the feature list surfaced to
// /api/v1/security/sandbox-status cannot drift apart.

package sandbox

import "errors"

// ErrLandlockRulesetRejected marks a landlock_create_ruleset(2) failure the
// boot path degrades to FallbackBackend. It wraps only EOPNOTSUPP (Landlock
// disabled) and EINVAL confirmed by a well-formedness probe to be an unknown
// rights bit (sandbox_linux.go::createRulesetErrnoIsDegradable); every other
// errno keeps hard-fail behaviour.
var ErrLandlockRulesetRejected = errors.New("landlock: kernel rejected ruleset creation")

// Landlock ABI v1 filesystem access rights (kernel 5.13+).
//
// Bit numbers and the ABI version that introduced each bit are pinned to
// include/uapi/linux/landlock.h in the upstream kernel (v6.10 read here):
//
//	REFER    (bit 13) — introduced by ABI v2 (kernel 5.19)
//	TRUNCATE (bit 14) — introduced by ABI v3 (kernel 6.2)
//	IOCTL_DEV (bit 15) — introduced by ABI v5 (kernel 6.10), NOT requested here
//
// IOCTL_DEV is left as a comment rather than a constant: it does not exist
// on kernels before 6.10, and asking the kernel for a bit it does not know
// returns EINVAL from landlock_create_ruleset — which is the failure mode
// Hard Constraint #4 requires us to avoid by degrading gracefully. The
// single place the running kernel's full rights mask is computed
// (landlockRightsForABI) reflects which ABI introduced each bit, so this
// list does not need to encode the version itself.
const (
	landlockAccessFSExecute    uint64 = 1 << 0
	landlockAccessFSWriteFile  uint64 = 1 << 1
	landlockAccessFSReadFile   uint64 = 1 << 2
	landlockAccessFSReadDir    uint64 = 1 << 3
	landlockAccessFSRemoveDir  uint64 = 1 << 4
	landlockAccessFSRemoveFile uint64 = 1 << 5
	landlockAccessFSMakeChar   uint64 = 1 << 6
	landlockAccessFSMakeDir    uint64 = 1 << 7
	landlockAccessFSMakeReg    uint64 = 1 << 8
	landlockAccessFSMakeSock   uint64 = 1 << 9
	landlockAccessFSMakeFifo   uint64 = 1 << 10
	landlockAccessFSMakeBlock  uint64 = 1 << 11
	landlockAccessFSMakeSym    uint64 = 1 << 12
	landlockAccessFSRefer      uint64 = 1 << 13 // ABI v2 (kernel 5.19).
	landlockAccessFSTruncate   uint64 = 1 << 14 // ABI v3 (kernel 6.2).
	// landlockAccessFSIoctlDev = 1 << 15, ABI v5 (kernel 6.10): not declared.
	// Adding an unknown bit here would land in every ruleset attr this
	// backend constructs, and the kernel would reject the attr with EINVAL
	// on every host below 6.10. landlockRightsForABI would include it on
	// ABI >= 5, so the constant can be added (and the include line
	// re-activated) once the supported kernel floor moves up.
)

// landlockFSBaseV1 is the set of Landlock filesystem access rights
// available from ABI v1 (kernel 5.13) onwards — every bit that exists in
// the kernel ABI from the very first release and never changes meaning.
// It is the seed of landlockRightsForABI; the rights added in later ABIs
// (REFER, TRUNCATE, IOCTL_DEV) are gated per-ABI there so this base
// stays a faithful representation of v1.
const landlockFSBaseV1 = landlockAccessFSExecute | landlockAccessFSWriteFile |
	landlockAccessFSReadFile | landlockAccessFSReadDir |
	landlockAccessFSRemoveDir | landlockAccessFSRemoveFile |
	landlockAccessFSMakeChar | landlockAccessFSMakeDir |
	landlockAccessFSMakeReg | landlockAccessFSMakeSock |
	landlockAccessFSMakeFifo | landlockAccessFSMakeBlock |
	landlockAccessFSMakeSym

// landlockFSWriteClassV1 is the write-class subset of landlockFSBaseV1 — the
// bits AccessWrite maps to from ABI v1 onward. accessToLandlockRights (in
// sandbox_linux.go) unions this with REFER/TRUNCATE and ANDs the result
// against lb.allRights, so referencing this named group instead of
// restating the same bit list keeps the write-class set in one place.
const landlockFSWriteClassV1 = landlockAccessFSWriteFile |
	landlockAccessFSRemoveDir | landlockAccessFSRemoveFile |
	landlockAccessFSMakeChar | landlockAccessFSMakeDir |
	landlockAccessFSMakeReg | landlockAccessFSMakeSock |
	landlockAccessFSMakeFifo | landlockAccessFSMakeBlock |
	landlockAccessFSMakeSym

// landlockFSDirOnlyV1 is the subset of v1 rights that only apply to
// directory file descriptors. addLandlockPathRule (in sandbox_linux.go)
// strips these from the access mask for regular / character / block /
// socket / fifo FDs, because the kernel rejects directory-only rights on
// a non-directory FD with EINVAL. REFER (ABI v2+) is added in
// addLandlockPathRule itself, derived from lb.allRights, so the strip
// mask tracks the ABI without duplicating the version logic here.
const landlockFSDirOnlyV1 = landlockAccessFSReadDir |
	landlockAccessFSRemoveDir | landlockAccessFSRemoveFile |
	landlockAccessFSMakeChar | landlockAccessFSMakeDir |
	landlockAccessFSMakeReg | landlockAccessFSMakeSock |
	landlockAccessFSMakeFifo | landlockAccessFSMakeBlock |
	landlockAccessFSMakeSym

// landlockRightsForABI returns the exact filesystem access-rights mask
// valid for the given Landlock ABI version — the single source of truth
// for which FS bits the running kernel knows. sandbox_linux.go's
// computeRights, the write-class branch of accessToLandlockRights, and the
// dirOnly strip mask in addLandlockPathRule all read from this one helper
// so the bits declared on landlock_create_ruleset, the bits OR'd onto an
// access-to-rule conversion, and the bits stripped from non-directory FDs
// cannot drift apart. Every bit requested is one the reported ABI has.
//
// Source: include/uapi/linux/landlock.h at v6.10 — REFER is ABI v2
// (kernel 5.19), TRUNCATE is ABI v3 (kernel 6.2), IOCTL_DEV is ABI v5
// (kernel 6.10). IOCTL_DEV is not declared as a constant in this file
// (see the const block above); when the supported kernel floor moves up
// to 6.10, declare it here and add
// `if abi >= 5 { rights |= landlockAccessFSIoctlDev }`.
func landlockRightsForABI(abi int) uint64 {
	rights := landlockFSBaseV1
	if abi >= 2 {
		rights |= landlockAccessFSRefer
	}
	if abi >= 3 {
		rights |= landlockAccessFSTruncate
	}
	return rights
}

// Landlock ABI v4 network access rights (kernel 6.7+). Used as the
// handledAccessNet mask on every LinuxBackend whose abiVersion >= 4; zero
// on older ABIs (the kernel rejects the field silently).
const (
	landlockAccessNetBindTcp    uint64 = 1 << 0
	landlockAccessNetConnectTcp uint64 = 1 << 1
)
