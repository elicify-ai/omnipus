package browser

// installer_sha256_test.go — coverage for ADR-052 M2 integrity:
// verifyChromeSHA256 (and the new findInstalledBuild chrome.sha256 hook)
// MUST reject a present-and-mismatching sha256 manifest so the managed
// download path retries on a corrupted package. A MISSING manifest stays
// permissive (the runtime-download path doesn't ship one, and older
// installs predate ADR-052 — refusing them would be a regression).
//
// Builds on installer_test.go's fixture style (seedBuildBinary lays out a
// fake installRoot/<version>/<build.subdir(platform)>/<binary>).

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// writeSHA256Manifest writes a chrome.sha256 manifest in installRoot whose
// hex digest matches the EXPECTED value (not necessarily the binary's
// actual digest — the mismatch path is exercised by overwriting after
// seeding the binary).
func writeSHA256Manifest(t *testing.T, installRoot, expectedHex string) string {
	t.Helper()
	shaPath := filepath.Join(installRoot, "chrome.sha256")
	require.NoError(t, os.WriteFile(shaPath, []byte(expectedHex+"\n"), 0o644))
	return shaPath
}

// hashBinary computes and returns the hex SHA-256 of path's contents.
func hashBinary(t *testing.T, path string) string {
	t.Helper()
	contents, err := os.ReadFile(path)
	require.NoError(t, err)
	sum := sha256.Sum256(contents)
	return hex.EncodeToString(sum[:])
}

// --- verifyChromeSHA256 ---

// TestVerifyChromeSHA256_Installer_HappyPath proves the canonical case: a
// binary at installRoot/<version>/... and a chrome.sha256 at installRoot
// whose digest matches — verifyChromeSHA256 returns nil.
func TestVerifyChromeSHA256_Installer_HappyPath(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	platform, err := cftPlatform()
	require.NoError(t, err)
	root := t.TempDir()
	binPath := seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())
	writeSHA256Manifest(t, root, hashBinary(t, binPath))

	assert.NoError(t, verifyChromeSHA256(binPath, filepath.Join(root, "chrome.sha256")))
}

// TestVerifyChromeSHA256_Installer_MismatchRejected covers the M2
// hard-fail direction: the manifest declares a wrong digest — the error
// must surface (no silent acceptance on mismatch per HC #6 — no silent
// defaults).
func TestVerifyChromeSHA256_Installer_MismatchRejected(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	platform, err := cftPlatform()
	require.NoError(t, err)
	root := t.TempDir()
	binPath := seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())
	// Write a manifest with a definitely-wrong digest.
	writeSHA256Manifest(t, root, "0000000000000000000000000000000000000000000000000000000000000000")

	err = verifyChromeSHA256(binPath, filepath.Join(root, "chrome.sha256"))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "sha256 mismatch")
}

// --- findInstalledBuild chrome.sha256 hook ---

// TestFindInstalledBuild_SHA256Mismatch_RejectsInstall is the M2
// regression guard for the managed installRoot path: when a
// chrome.sha256 manifest is present and disagrees with the binary's
// actual SHA-256, findInstalledBuild must return "" (treating the build
// as "not installed") so EnsureChromiumBuild's download path retries.
// This is the package-vs-runtime-download integrity contract ADR-052
// D2/M2 — at least as strong as runtime-download, never weaker.
func TestFindInstalledBuild_SHA256Mismatch_RejectsInstall(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	platform, err := cftPlatform()
	require.NoError(t, err)
	root := t.TempDir()
	binPath := seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())
	// Write a manifest that does NOT match the binary's actual digest.
	writeSHA256Manifest(t, root, "0000000000000000000000000000000000000000000000000000000000000000")

	got := findInstalledBuild(root, platform, fullChromeBuild())
	assert.Empty(t, got, "findInstalledBuild must return \"\" when chrome.sha256 disagrees with the binary's actual SHA-256")
	// Sanity: the mismatched binary is still on disk, just rejected.
	_, statErr := os.Stat(binPath)
	assert.NoError(t, statErr)
}

// TestFindInstalledBuild_SHA256Match_AcceptsInstall is the happy-path
// counterpart: a manifest whose digest matches the binary's actual
// SHA-256 — findInstalledBuild returns the binary's path normally.
func TestFindInstalledBuild_SHA256Match_AcceptsInstall(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	platform, err := cftPlatform()
	require.NoError(t, err)
	root := t.TempDir()
	binPath := seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())
	writeSHA256Manifest(t, root, hashBinary(t, binPath))

	got := findInstalledBuild(root, platform, fullChromeBuild())
	assert.Equal(t, binPath, got)
}

// TestFindInstalledBuild_SHAMissing_StillAccepts proves the permissive
// direction (a missing manifest must NOT regress pre-ADR-052 installs):
// no chrome.sha256 at installRoot — findInstalledBuild returns the
// binary's path normally, mirroring its pre-M2 behavior exactly. This
// is the back-compat invariant: refusing a binary because its package
// lacks integrity metadata would be a regression, since the runtime-
// download path itself doesn't ship a sha256.
func TestFindInstalledBuild_SHAMissing_StillAccepts(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	platform, err := cftPlatform()
	require.NoError(t, err)
	root := t.TempDir()
	binPath := seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())
	// Deliberately no chrome.sha256 in installRoot.

	got := findInstalledBuild(root, platform, fullChromeBuild())
	assert.Equal(t, binPath, got, "missing chrome.sha256 must NOT block findInstalledBuild (back-compat with pre-ADR-052 installs)")
}

// TestFindInstalledBuild_SHAMalformed_RejectsInstall proves the malformed-
// manifest guard: a chrome.sha256 containing non-hex content is treated as
// "mismatch" — findInstalledBuild returns "" rather than silently accepting
// a binary whose integrity can't be confirmed. Per HC #6 — no silent
// defaults, including silent acceptance of malformed integrity input.
func TestFindInstalledBuild_SHAMalformed_RejectsInstall(t *testing.T) {
	if _, err := cftPlatform(); err != nil {
		t.Skipf("unsupported platform: %v", err)
	}
	platform, err := cftPlatform()
	require.NoError(t, err)
	root := t.TempDir()
	seedBuildBinary(t, root, "131.0.6778.108", platform, fullChromeBuild())
	// Manifest with garbage content.
	writeSHA256Manifest(t, root, "not-a-hex-digest")

	got := findInstalledBuild(root, platform, fullChromeBuild())
	assert.Empty(t, got, "malformed chrome.sha256 must cause findInstalledBuild to refuse the build (no silent accept)")
}

// TestChromiumBuild_SHA256Path_HelperContract is a tight unit test on the
// chromiumBuild.sha256Path helper: installRoot=="" returns "" (defensive);
// otherwise it returns <installRoot>/chrome.sha256. Single source of truth
// for the manifest's on-disk location; future relayouts update one method.
func TestChromiumBuild_SHA256Path_HelperContract(t *testing.T) {
	assert.Empty(t, fullChromeBuild().sha256Path(""))
	assert.Equal(t,
		filepath.Join("/opt/omnipus", "chrome.sha256"),
		fullChromeBuild().sha256Path("/opt/omnipus"),
	)
	// headlessShellBuild uses the same manifest (one manifest per
	// installRoot, regardless of which build it accompanies).
	assert.Equal(t,
		filepath.Join("/opt/omnipus", "chrome.sha256"),
		headlessShellBuild().sha256Path("/opt/omnipus"),
	)
}
