package thirdpartycli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The Q72 coverage brief requires an unreadable filesystem top to remain an
// error, never a successful "outside" decision. Windows can supply a real
// unavailable drive root without changing the filesystem or mocking Lstat.
// Unix has only /, so inducing this branch there would need an unsafe root
// change or a production test seam. This test is Windows-specific, not skipped.
func TestT11Resolver_FailedFilesystemTopReadIsAnError(t *testing.T) {
	var top string
	for drive := 'Z'; drive >= 'A'; drive-- {
		candidate := string(drive) + `:\`
		if _, statErr := os.Lstat(candidate); statErr != nil {
			top = candidate
			break
		}
	}
	require.NotEmpty(t, top, "PREREQUISITE: this Windows account needs an unavailable drive root for the real read-failure instrument")
	_, controlErr := os.Lstat(top)
	var controlPathError *os.PathError
	require.ErrorAs(t, controlErr, &controlPathError, "instrument: the actual filesystem-top read fails")
	entered := filepath.Join(top, "t11-top-probe", "codex.exe")
	// Bind the current identity-aware function directly. The obsolete
	// two-argument compatibility branch is not part of this landed contract.
	inside, checkErr := insideAny(entered, nil, nil)
	assert.False(t, inside, "a failed top read cannot claim membership")
	var pathError *os.PathError
	require.ErrorAs(t, checkErr, &pathError, "Q72: propagate the real failed top read, never false,nil")
	assert.Equal(t, top, pathError.Path, "Q72: the failed read was of the filesystem top, not a fabricated child")
	assert.ErrorIs(t, checkErr, controlPathError.Err, "the operating-system failure survives wrapping")
}
