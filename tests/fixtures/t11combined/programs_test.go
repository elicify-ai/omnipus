package t11combined

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Q49 reconciliation: trusted fixtures really live outside the writable runtime
// temp root. Changing TMPDIR/TMP/TEMP cannot stand in for moving the fixture.
func TestTrustedProgramDir_OutsideRuntimeTempWithoutEnvironmentOverride_Q49(t *testing.T) {
	temp, err := filepath.EvalSymlinks(os.TempDir())
	require.NoError(t, err)
	var dir string
	t.Run("real_location_and_unchanged_environment", func(t *testing.T) {
		type value struct {
			text string
			set  bool
		}
		before := make(map[string]value)
		for _, key := range []string{"TMPDIR", "TMP", "TEMP"} {
			text, set := os.LookupEnv(key)
			before[key] = value{text, set}
		}
		dir = TrustedProgramDir(t)
		for key, want := range before {
			text, set := os.LookupEnv(key)
			assert.Equal(t, want, value{text, set}, "Q49: %s must not be changed to make a program trusted", key)
		}
		assert.DirExists(t, dir)
		canonical, err := filepath.EvalSymlinks(dir)
		require.NoError(t, err)
		assert.Equal(t, canonical, dir, "the fixture returns the physical directory")
		// The founder explicitly excludes the shared Unix temp folder from the
		// writable union. A per-user/custom runtime temp is not that exception.
		shared := runtime.GOOS != "windows" && (temp == "/tmp" || temp == "/private/tmp")
		if !shared && strings.EqualFold(filepath.VolumeName(temp), filepath.VolumeName(dir)) {
			rel, err := filepath.Rel(temp, dir)
			require.NoError(t, err)
			assert.True(t, rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)),
				"Q49: fixture %q must really be outside original runtime temp %q", dir, temp)
		}
	})
	_, err = os.Stat(dir)
	assert.ErrorIs(t, err, os.ErrNotExist, "the fixture directory is removed after its owning test")
}
