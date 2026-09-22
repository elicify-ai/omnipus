//go:build darwin

// Darwin-specific tests for missingChromeLibsMachO (the file-opening +
// path-resolution wrapper in command_libs_darwin.go). These tests verify
// the dylib resolution logic (@rpath always-present, absolute paths via
// os.Stat, bare names via search paths) against the real macOS filesystem.
//
// The pure byte-level Mach-O parser tests (parseMachODylibs) live in
// command_libs_macho_test.go and run on ALL platforms including Linux.

package doctor

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestMissingChromeLibsMachO_PresentSystemLib verifies that a Mach-O
// referencing a real macOS system library (/usr/lib/libSystem.B.dylib)
// resolves cleanly (no missing libs reported).
func TestMissingChromeLibsMachO_PresentSystemLib(t *testing.T) {
	// /usr/lib/libSystem.B.dylib exists on every macOS install.
	macho := buildMinimalMachO64(t, "/usr/lib/libSystem.B.dylib")
	path := filepath.Join(t.TempDir(), "chrome")
	require.NoError(t, os.WriteFile(path, macho, 0o755))

	missing, err := missingChromeLibsMachO(path)
	require.NoError(t, err)
	assert.Empty(t, missing, "libSystem.B.dylib should resolve on macOS")
}

// TestMissingChromeLibsMachO_MissingLib verifies that a Mach-O
// referencing a non-existent absolute path surfaces as missing.
func TestMissingChromeLibsMachO_MissingLib(t *testing.T) {
	missingLib := "/usr/lib/libtotally_nonexistent_fake.42.dylib"
	macho := buildMinimalMachO64(t, missingLib)
	path := filepath.Join(t.TempDir(), "chrome")
	require.NoError(t, os.WriteFile(path, macho, 0o755))

	missing, err := missingChromeLibsMachO(path)
	require.NoError(t, err)
	assert.Contains(t, missing, missingLib)
}

// TestMissingChromeLibsMachO_RpathSkipped verifies that @rpath/ dylib
// names are treated as always-present (the .app bundle resolves them at
// runtime).
func TestMissingChromeLibsMachO_RpathSkipped(t *testing.T) {
	macho := buildMinimalMachO64(t, "@rpath/Chromium Framework.framework/Libraries/libEGL.dylib")
	path := filepath.Join(t.TempDir(), "chrome")
	require.NoError(t, os.WriteFile(path, macho, 0o755))

	missing, err := missingChromeLibsMachO(path)
	require.NoError(t, err)
	assert.Empty(t, missing, "@rpath/ dylibs should be treated as present")
}

// TestMissingChromeLibsMachO_ExecutablePathSkipped verifies that
// @executable_path/ dylib names are treated as always-present.
func TestMissingChromeLibsMachO_ExecutablePathSkipped(t *testing.T) {
	macho := buildMinimalMachO64(t, "@executable_path/libhelper.dylib")
	path := filepath.Join(t.TempDir(), "chrome")
	require.NoError(t, os.WriteFile(path, macho, 0o755))

	missing, err := missingChromeLibsMachO(path)
	require.NoError(t, err)
	assert.Empty(t, missing, "@executable_path/ dylibs should be treated as present")
}

// TestMissingChromeLibsMachO_MixedPresentAndMissing verifies that a
// Mach-O with both present and missing dylibs surfaces only the missing
// ones.
func TestMissingChromeLibsMachO_MixedPresentAndMissing(t *testing.T) {
	macho := buildMachO64WithDylibs(t, []string{
		"/usr/lib/libSystem.B.dylib",                   // present
		"@rpath/Chromium Framework.framework/Chromium", // present (@rpath)
		"/usr/lib/libfake_missing_xyz.dylib",           // missing
	})
	path := filepath.Join(t.TempDir(), "chrome")
	require.NoError(t, os.WriteFile(path, macho, 0o755))

	missing, err := missingChromeLibsMachO(path)
	require.NoError(t, err)
	require.Len(t, missing, 1)
	assert.Equal(t, "/usr/lib/libfake_missing_xyz.dylib", missing[0])
}

// TestMissingChromeLibsMachO_NonMachO verifies that a non-Mach-O file
// returns the synthetic "not-a-macho-binary" entry.
func TestMissingChromeLibsMachO_NonMachO(t *testing.T) {
	path := filepath.Join(t.TempDir(), "chrome")
	require.NoError(t, os.WriteFile(path, []byte{0xDE, 0xAD, 0xBE, 0xEF}, 0o755))

	missing, err := missingChromeLibsMachO(path)
	require.NoError(t, err)
	assert.Equal(t, []string{notAMachOBinary}, missing)
}

// TestMissingChromeLibsMachO_FileNotFound verifies that opening a
// non-existent binary path returns an error.
func TestMissingChromeLibsMachO_FileNotFound(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nonexistent")
	_, err := missingChromeLibsMachO(path)
	assert.Error(t, err)
}

// TestDylibResolves_RpathPrefix verifies the resolution logic directly.
func TestDylibResolves_RpathPrefix(t *testing.T) {
	assert.True(t, dylibResolves("@rpath/libfoo.dylib"))
	assert.True(t, dylibResolves("@executable_path/libbar.dylib"))
	assert.True(t, dylibResolves("@loader_path/libbaz.dylib"))
}

// TestDylibResolves_AbsolutePath verifies that existing absolute paths
// resolve and non-existing ones do not.
func TestDylibResolves_AbsolutePath(t *testing.T) {
	// /usr/lib/libSystem.B.dylib exists on every macOS install.
	assert.True(t, dylibResolves("/usr/lib/libSystem.B.dylib"))
	// Non-existent path.
	assert.False(t, dylibResolves("/usr/lib/libnonexistent_xyz.999.dylib"))
}
