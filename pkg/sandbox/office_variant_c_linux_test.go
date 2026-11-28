//go:build linux

package sandbox

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"golang.org/x/sys/unix"
)

// T-118 / spec 4763de351 §3.6 D3 and FR-061. The oracle is the kernel
// header, through x/sys/unix, NOT the backend's constants or computeRights.
// The abstract access value's name/bit are expressly the developer's choice.
func TestOfficeVariantC_T118_ExactKernelRightsAndMacOSRefusal(t *testing.T) {
	const wanted = uint64(unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE)
	var access uint64
	probe := LinuxBackend{allRights: ^uint64(0)}
	for bit := uint(0); bit < 64; bit++ {
		candidate := uint64(1) << bit
		if candidate&(AccessRead|AccessWrite|AccessExecute) != 0 {
			continue
		}
		mapped := probe.accessToLandlockRights(candidate)
		if mapped != 0 {
			require.Equal(t, wanted, mapped, "T-118: any new path access must be EXACTLY the independent kernel mask, no hidden wider value")
			require.Zero(t, access, "T-118: exactly ONE new abstract access value, not aliases")
			access = candidate
		}
	}
	if access == 0 {
		t.Fatal("BLOCKED: no new path-rule access maps to kernel MAKE_SOCK|REMOVE_FILE (0x220) — required by FR-061 / T-118")
	}
	t.Logf("T-118: developer-selected abstract access=%#x; independent kernel mask=%#x", access, wanted)
	// Both rights exist in ABI 1. Later ABI rights must never leak into it.
	for _, abi := range []int{1, 2, 3, 4, 5, 6} {
		t.Run(fmt.Sprintf("ABI-%d", abi), func(t *testing.T) {
			backend := LinuxBackend{abiVersion: abi, allRights: ^uint64(0)}
			assert.Equal(t, wanted, backend.accessToLandlockRights(access), "T-118: exact mask, no broad write rights")
			assert.Equal(t, wanted|uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE|unix.LANDLOCK_ACCESS_FS_READ_DIR), backend.accessToLandlockRights(access|AccessRead), "T-118: read flag composes without losing/adding rights")
			assert.Equal(t, wanted|uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE), backend.accessToLandlockRights(access|AccessExecute), "T-118: execute flag composes without broadening writes")
		})
	}
	t.Run("macOS-rejects-narrow-access", func(t *testing.T) {
		profile, err := renderSeatbeltProfile(SandboxPolicy{FilesystemRules: []PathRule{
			{Path: "/tmp", Access: AccessRead}, // Positive allow keeps the zero-allows guard irrelevant.
			{Path: "/tmp", Access: access},
		}})
		assert.Error(t, err, "T-118: macOS must refuse the Linux-only value, not silently discard it")
		assert.Empty(t, profile, "T-118: refused policy must produce NO usable profile")
	})
}
