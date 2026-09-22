// Omnipus - Ultra-lightweight personal AI gateway
// License: MIT
// Copyright (c) 2026 Omnipus contributors

// Platform-neutral tests for the Landlock ABI gating logic. These run on
// every supported OS so the rights-mask table is verified at boot on
// Darwin CI runners and on developer machines that are not running Linux.
//
// The table tests pin the exact mask every ABI version returns, and the
// absence tests pin the specific bits that would cause the kernel to
// reject the ruleset — so a regression reads as a pointed diagnostic, not
// as a generic mask mismatch.

package sandbox

import (
	"strconv"
	"testing"
)

// TestLandlockRightsForABI_TablePerABIVersion is the core regression table:
// every bit the running kernel does not know must be absent from the
// rights mask. Each row pins one ABI version and asserts the exact set of
// FS rights the helper returns. Reading the table left
// to right shows precisely which kernel release added which bit:
//
//	v1 (kernel 5.13) — base 13 bits (EXECUTE..MAKE_SYM), no REFER
//	v2 (kernel 5.19) — + LANDLOCK_ACCESS_FS_REFER
//	v3 (kernel 6.2)  — + LANDLOCK_ACCESS_FS_TRUNCATE
//	v4 (kernel 6.7)  — + NET_BIND_TCP / NET_CONNECT_TCP (handledAccessNet)
//	v5 (kernel 6.10) — + LANDLOCK_ACCESS_FS_IOCTL_DEV (declared as a comment)
//	v6 (kernel 6.12) — + scoping (LANDLOCK_SCOPE_*) — no FS bit, so rights
//	                   mask stays equal to v5 here
//
// The "IOCTL_DEV" bit (1<<15) is intentionally NOT a declared constant in
// landlock_abi_rights.go (the const block documents why — kernel 6.8.0-107
// the code was tested on does not expose it, and adding an unknown bit
// causes EINVAL from create_ruleset on every pre-6.10 host). When the
// supported kernel floor moves up to 6.10, the constant must be
// re-declared and the v5/v6 rows here extended.
func TestLandlockRightsForABI_TablePerABIVersion(t *testing.T) {
	const baseV1 = landlockFSBaseV1
	const wantV1 = baseV1
	const wantV2 = baseV1 | landlockAccessFSRefer
	const wantV3 = baseV1 | landlockAccessFSRefer | landlockAccessFSTruncate
	const wantV4 = baseV1 | landlockAccessFSRefer | landlockAccessFSTruncate
	const wantV5 = baseV1 | landlockAccessFSRefer | landlockAccessFSTruncate
	const wantV6 = baseV1 | landlockAccessFSRefer | landlockAccessFSTruncate

	cases := []struct {
		name        string
		abi         int
		wantRights  uint64
		wantHandled uint64 // handledAccessNet mask (separate from FS).
	}{
		{"abi_v1_kernel_5_13", 1, wantV1, 0},
		{"abi_v2_kernel_5_19_refer", 2, wantV2, 0},
		{"abi_v3_kernel_6_2_truncate", 3, wantV3, 0},
		{"abi_v4_kernel_6_7_net", 4, wantV4, landlockAccessNetBindTcp | landlockAccessNetConnectTcp},
		{"abi_v5_kernel_6_10_ioctl", 5, wantV5, landlockAccessNetBindTcp | landlockAccessNetConnectTcp},
		{"abi_v6_kernel_6_12_scoping", 6, wantV6, landlockAccessNetBindTcp | landlockAccessNetConnectTcp},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := landlockRightsForABI(tc.abi)
			if got != tc.wantRights {
				t.Errorf("landlockRightsForABI(%d) = %#016x, want %#016x (extra bits set would EINVAL create_ruleset on this ABI)",
					tc.abi, got, tc.wantRights)
			}
		})
	}
}

// TestLandlockRightsForABI_AbsenceIsRegressionGuard asserts the SPECIFIC
// absence properties each ABI must satisfy. Split out from the equality
// table above so a regression reads as a pointed diagnostic, not as a
// generic "mismatch".
func TestLandlockRightsForABI_AbsenceIsRegressionGuard(t *testing.T) {
	cases := []struct {
		name        string
		abi         int
		absent      []uint64
		absentNames []string
	}{
		{
			// REFER (bit 13) is ABI v2; a v1 kernel does not know this bit.
			name:        "abi_v1_refer_absent",
			abi:         1,
			absent:      []uint64{landlockAccessFSRefer},
			absentNames: []string{"REFER"},
		},
		{
			// TRUNCATE (bit 14) is ABI v3; a v2 kernel does not know this bit.
			name:        "abi_v2_truncate_absent",
			abi:         2,
			absent:      []uint64{landlockAccessFSTruncate},
			absentNames: []string{"TRUNCATE"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := landlockRightsForABI(tc.abi)
			for i, bit := range tc.absent {
				if got&bit != 0 {
					t.Errorf("landlockRightsForABI(%d) set bit %s (%#x); kernel reported ABI v%d does not know this bit, "+
						"so create_ruleset would return EINVAL",
						tc.abi, tc.absentNames[i], bit, tc.abi)
				}
			}
		})
	}
}

// TestDetectLandlockABI_GatesMatchKernelHeaders pins the status-endpoint
// feature list to the same per-ABI gates the rights-mask helper uses. The
// two tables (landlockRightsForABI in this file, DetectLandlockABI in
// sandbox.go) MUST agree on which ABI introduced which bit — a kernel that
// reports ABI vN must never see a feature the ruleset does not also
// request for that ABI.
func TestDetectLandlockABI_GatesMatchKernelHeaders(t *testing.T) {
	cases := []struct {
		abi          int
		wantFeats    []string // features that MUST be present.
		mustNotFeats []string // features that MUST be absent.
	}{
		{
			abi: 1,
			wantFeats: []string{
				"EXECUTE", "WRITE_FILE", "READ_FILE", "READ_DIR",
				"REMOVE_DIR", "REMOVE_FILE", "MAKE_CHAR", "MAKE_DIR",
				"MAKE_REG", "MAKE_SOCK", "MAKE_FIFO", "MAKE_BLOCK", "MAKE_SYM",
			},
			mustNotFeats: []string{
				"REFER", "TRUNCATE", "IOCTL_DEV", "NET_BIND_TCP", "NET_CONNECT_TCP",
			},
		},
		{
			abi:       2,
			wantFeats: []string{"REFER"},
			mustNotFeats: []string{
				"TRUNCATE", "IOCTL_DEV", "NET_BIND_TCP", "NET_CONNECT_TCP",
			},
		},
		{
			abi:       3,
			wantFeats: []string{"REFER", "TRUNCATE"},
			mustNotFeats: []string{
				"IOCTL_DEV", "NET_BIND_TCP", "NET_CONNECT_TCP",
			},
		},
		{
			abi:          4,
			wantFeats:    []string{"REFER", "TRUNCATE", "NET_BIND_TCP", "NET_CONNECT_TCP"},
			mustNotFeats: []string{"IOCTL_DEV"},
		},
		{
			abi:          5,
			wantFeats:    []string{"REFER", "TRUNCATE", "NET_BIND_TCP", "NET_CONNECT_TCP", "IOCTL_DEV"},
			mustNotFeats: []string{},
		},
		{
			abi:          6,
			wantFeats:    []string{"REFER", "TRUNCATE", "NET_BIND_TCP", "NET_CONNECT_TCP", "IOCTL_DEV"},
			mustNotFeats: []string{},
		},
	}

	for _, tc := range cases {
		t.Run("abi_v"+strconv.Itoa(tc.abi), func(t *testing.T) {
			got := DetectLandlockABI(tc.abi)
			if !got.Available {
				t.Fatalf("DetectLandlockABI(%d) reported unavailable; want available", tc.abi)
			}
			if got.Version != tc.abi {
				t.Errorf("Version = %d, want %d", got.Version, tc.abi)
			}
			feats := append([]string{}, got.Features...)
			// presence
			for _, want := range tc.wantFeats {
				if !containsFeature(feats, want) {
					t.Errorf("ABI v%d must include feature %q; got %v", tc.abi, want, feats)
				}
			}
			// absence — the strict half: a feature list that passes
			// presence but includes a later-ABI feature is the drift these
			// gates exist to prevent.
			for _, banned := range tc.mustNotFeats {
				if containsFeature(feats, banned) {
					t.Errorf("ABI v%d must NOT include feature %q (added in a later ABI); got %v",
						tc.abi, banned, feats)
				}
			}
		})
	}
}

// containsFeature is a tiny helper to avoid pulling slices.Contains in
// for a 2-line use in the feature-list assertions. (sibling_grants_test.go
// already declares an unexported `contains`, so the names must differ.)
func containsFeature(s []string, x string) bool {
	for _, v := range s {
		if v == x {
			return true
		}
	}
	return false
}

// TestLandlockDerivedMasksV1_StayInsideTheV1Table pins the two derived
// rights groups in landlock_abi_rights.go against the v1 table they are
// carved out of. Both are plain bit lists restated by hand, and both are
// consumed only from sandbox_linux.go — so on a non-Linux developer machine
// nothing reads them at all and a drifted bit reaches CI unexamined.
//
// What each one would break if it drifted:
//
//   - landlockFSWriteClassV1 is what AccessWrite expands to in
//     sandbox_linux.go::accessToLandlockRights. A read bit leaking in
//     over-grants: a write-only path rule would also permit reads.
//   - landlockFSDirOnlyV1 is STRIPPED from the access mask for every
//     non-directory file descriptor in addLandlockPathRule, because the
//     kernel EINVALs a directory-only right on a regular-file fd. A
//     file-applicable bit leaking in silently removes a right the operator's
//     policy asked for — the quiet half of the failure, with no errno.
//
// Both are checked against landlockRightsForABI(1) rather than against a
// second hand-written list, so this test cannot drift with them.
func TestLandlockDerivedMasksV1_StayInsideTheV1Table(t *testing.T) {
	v1 := landlockRightsForABI(1)

	cases := []struct {
		name string
		mask uint64
		// mustSet are bits the group is defined to carry.
		mustSet map[string]uint64
		// mustClear are bits whose presence is the defect described above.
		mustClear map[string]uint64
	}{
		{
			name: "landlockFSWriteClassV1",
			mask: landlockFSWriteClassV1,
			mustSet: map[string]uint64{
				"WRITE_FILE":  landlockAccessFSWriteFile,
				"REMOVE_FILE": landlockAccessFSRemoveFile,
				"REMOVE_DIR":  landlockAccessFSRemoveDir,
				"MAKE_REG":    landlockAccessFSMakeReg,
				"MAKE_DIR":    landlockAccessFSMakeDir,
				"MAKE_SYM":    landlockAccessFSMakeSym,
			},
			mustClear: map[string]uint64{
				"READ_FILE": landlockAccessFSReadFile,
				"READ_DIR":  landlockAccessFSReadDir,
				"EXECUTE":   landlockAccessFSExecute,
				// REFER/TRUNCATE are unioned in at the call site, gated on
				// the running ABI. Baking them into the v1 group would
				// request them on a kernel that does not know them.
				"REFER":    landlockAccessFSRefer,
				"TRUNCATE": landlockAccessFSTruncate,
			},
		},
		{
			name: "landlockFSDirOnlyV1",
			mask: landlockFSDirOnlyV1,
			mustSet: map[string]uint64{
				"READ_DIR":    landlockAccessFSReadDir,
				"REMOVE_DIR":  landlockAccessFSRemoveDir,
				"REMOVE_FILE": landlockAccessFSRemoveFile,
				"MAKE_REG":    landlockAccessFSMakeReg,
				"MAKE_DIR":    landlockAccessFSMakeDir,
			},
			mustClear: map[string]uint64{
				// These three apply to a regular-file fd. Stripping them
				// from one would silently narrow the policy.
				"READ_FILE":  landlockAccessFSReadFile,
				"WRITE_FILE": landlockAccessFSWriteFile,
				"EXECUTE":    landlockAccessFSExecute,
				"REFER":      landlockAccessFSRefer,
				"TRUNCATE":   landlockAccessFSTruncate,
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if extra := tc.mask &^ v1; extra != 0 {
				t.Errorf("%s = %#016x carries %#016x outside landlockRightsForABI(1) = %#016x; "+
					"a bit no v1 kernel knows would EINVAL create_ruleset on kernels 5.13-5.18",
					tc.name, tc.mask, extra, v1)
			}
			for name, bit := range tc.mustSet {
				if tc.mask&bit == 0 {
					t.Errorf("%s is missing %s (%#x)", tc.name, name, bit)
				}
			}
			for name, bit := range tc.mustClear {
				if tc.mask&bit != 0 {
					t.Errorf("%s must not carry %s (%#x); see this test's doc comment for what that breaks",
						tc.name, name, bit)
				}
			}
		})
	}
}
