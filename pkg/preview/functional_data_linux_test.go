//go:build linux

package preview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// O2 / T-114..T-118: variant C's remove right covers /tmp descendants.
// No test home, owner record, source install or outside control may live there.
func functionalPinDataOutsideTmp(t *testing.T) {
	t.Helper()
	base := os.Getenv("OMNIPUS_TEST_OFFICE_DATA_ROOT")
	if base == "" {
		var err error
		base, err = os.Getwd()
		require.NoError(t, err)
	}
	require.True(t, filepath.IsAbs(base), "O2: set OMNIPUS_TEST_OFFICE_DATA_ROOT to an absolute directory outside /tmp")
	require.NoError(t, os.MkdirAll(base, 0700))
	resolved, err := filepath.EvalSymlinks(base)
	require.NoError(t, err)
	tmp, err := filepath.EvalSymlinks("/tmp")
	require.NoError(t, err)
	require.False(t, resolved == tmp || strings.HasPrefix(resolved, tmp+string(os.PathSeparator)), "O2: home/data root resolves inside /tmp: %s", resolved)
	owned, err := os.MkdirTemp(resolved, "office-functional-")
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, os.RemoveAll(owned), "remove only this test's owned data folder") })
	t.Setenv("TMPDIR", owned)
	t.Logf("O2: test homes/data outside /tmp: %s", owned)
}
