//go:build linux

// Omnipus - Ultra-lightweight personal AI gateway
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Linux-only tests for the LinuxBackend ABI gating. The pure-helper tests
// for landlockRightsForABI / DetectLandlockABI live in
// landlock_abi_gating_test.go (no build tag) so the gating logic is
// covered on every CI runner; this file adds the LinuxBackend-method
// coverage that needs the linux-tagged struct.
//
// computeRights and accessToLandlockRights are exercised here against a
// fresh LinuxBackend per ABI version to prove every bit they emit is one
// the reported ABI has.

package sandbox

import (
	"strconv"
	"testing"

	"golang.org/x/sys/unix"
)

// TestLinuxBackend_ComputeRights_MatchesHelper pins computeRights to the
// helper: any drift between the two is caught here. Two routes through the
// rights mask must agree on the bits — computeRights seeds lb.allRights
// (used on create_ruleset) AND accessToLandlockRights reads from the
// same field for write-class rights, so a divergence would surface as a
// ruleset attr the kernel rejects.
func TestLinuxBackend_ComputeRights_MatchesHelper(t *testing.T) {
	for _, abi := range []int{1, 2, 3, 4, 5, 6} {
		t.Run("abi_v"+strconv.Itoa(abi), func(t *testing.T) {
			lb := &LinuxBackend{abiVersion: abi}
			lb.computeRights()
			if lb.allRights != landlockRightsForABI(abi) {
				t.Errorf("computeRights().allRights = %#016x, want landlockRightsForABI(%d) = %#016x",
					lb.allRights, abi, landlockRightsForABI(abi))
			}
		})
	}
}

// TestAccessToLandlockRights_DoesNotExceedKernelRights confirms that an
// AccessWrite rule never produces a rights mask with bits the running
// kernel does not know. accessToLandlockRights must agree with
// computeRights about what the kernel accepts, so an ABI-mismatched
// kernel cannot have a rule silently rejected at landlock_add_rule after
// passing create_ruleset.
func TestAccessToLandlockRights_DoesNotExceedKernelRights(t *testing.T) {
	cases := []struct {
		abi      int
		access   uint64
		describe string
	}{
		{1, AccessWrite, "abi_v1_AccessWrite"},
		{1, AccessRead | AccessWrite | AccessExecute, "abi_v1_full_access"},
		{2, AccessWrite, "abi_v2_AccessWrite"},
		{3, AccessWrite, "abi_v3_AccessWrite"},
	}
	for _, tc := range cases {
		t.Run(tc.describe, func(t *testing.T) {
			lb := &LinuxBackend{abiVersion: tc.abi}
			lb.computeRights()
			got := lb.accessToLandlockRights(tc.access)
			// Every bit the helper returned MUST also be in lb.allRights
			// (the kernel-known set). The reverse is not required — an
			// access flag may legitimately map to a subset (AccessRead
			// only sets READ_FILE + READ_DIR).
			if got&^lb.allRights != 0 {
				t.Errorf("accessToLandlockRights(%#x) on ABI v%d returned bits %#x that are NOT in lb.allRights (%#016x) — "+
					"these would make landlock_add_rule fail with EINVAL on the running kernel",
					tc.access, tc.abi, got&^lb.allRights, lb.allRights)
			}
		})
	}
}

// TestAccessToLandlockRights_AbsenceIsRegressionGuard asserts the
// specific bit-absence properties the accessToLandlockRights path must
// satisfy: AccessWrite on ABI v1 must not include REFER (bit 13); on ABI
// v2 it must not include TRUNCATE (bit 14). A regression here would mean
// the ruleset attr landlock_add_rule receives has a bit the running
// kernel does not know — an EINVAL on the rule-add syscall.
func TestAccessToLandlockRights_AbsenceIsRegressionGuard(t *testing.T) {
	cases := []struct {
		name        string
		abi         int
		absent      []uint64
		absentNames []string
	}{
		{
			name:        "abi_v1_AccessWrite_no_refer",
			abi:         1,
			absent:      []uint64{landlockAccessFSRefer},
			absentNames: []string{"REFER"},
		},
		{
			name:        "abi_v2_AccessWrite_no_truncate",
			abi:         2,
			absent:      []uint64{landlockAccessFSTruncate},
			absentNames: []string{"TRUNCATE"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			lb := &LinuxBackend{abiVersion: tc.abi}
			lb.computeRights()
			got := lb.accessToLandlockRights(AccessWrite)
			for i, bit := range tc.absent {
				if got&bit != 0 {
					t.Errorf("accessToLandlockRights(AccessWrite) on ABI v%d set bit %s (%#x); "+
						"the running kernel does not know this bit, so landlock_add_rule "+
						"would return EINVAL",
						tc.abi, tc.absentNames[i], bit)
				}
			}
		})
	}
}

// TestCreateRulesetErrnoIsDegradable pins the boot path's degrade decision for
// a landlock_create_ruleset(2) errno. EOPNOTSUPP always degrades. EINVAL is
// ambiguous — the kernel returns it for an unknown rights bit and for a
// malformed attr struct — so it degrades only when the attr is proven
// well-formed; an EINVAL with a failed probe is a struct-shape bug and
// hard-fails. Every other errno hard-fails.
//
// No real kernel is needed: unix.Errno is a plain comparable value type, so
// every case here is a pure function call.
func TestCreateRulesetErrnoIsDegradable(t *testing.T) {
	cases := []struct {
		name           string
		errno          unix.Errno
		attrWellFormed bool
		degrade        bool
	}{
		{"EINVAL_well_formed_degrades", unix.EINVAL, true, true},
		{"EINVAL_malformed_hard_fails", unix.EINVAL, false, false},
		{"EOPNOTSUPP_degrades_regardless_of_probe", unix.EOPNOTSUPP, false, true},
		{"EFAULT_hard_fails", unix.EFAULT, true, false},
		{"E2BIG_hard_fails", unix.E2BIG, true, false},
		{"EPERM_hard_fails", unix.EPERM, true, false},
		{"ENOSYS_hard_fails", unix.ENOSYS, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := createRulesetErrnoIsDegradable(tc.errno, tc.attrWellFormed)
			if got != tc.degrade {
				t.Errorf("createRulesetErrnoIsDegradable(%v, %v) = %v, want %v",
					tc.errno, tc.attrWellFormed, got, tc.degrade)
			}
		})
	}
}
