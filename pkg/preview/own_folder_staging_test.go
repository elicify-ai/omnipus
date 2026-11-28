package preview

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// functionalOwnFolder stages the default program path's own folder — the
// §3.9 sanctioned fixture move (office-preview spec, founder decision OF):
// on Linux the own-folder check for preview programs is live, so every
// fixture here that registers a component or writes a registered manifest
// stages inside it. The folder is derived from the RUNNING TEST BINARY
// exactly as the default resolver derives it (dir(resolved)/../share/omnipus),
// so the registration and the resolver that judges the fixture agree on the
// same own folder. Assertions are untouched at every call site; only the
// fixture roots relocate. On darwin and windows no own-folder gate exists
// (decision OF is Linux-scoped) and this is an ordinary staging folder.
func functionalOwnFolder(t *testing.T) string {
	t.Helper()
	self, err := os.Executable()
	require.NoError(t, err)
	resolved, err := filepath.EvalSymlinks(self)
	require.NoError(t, err)
	own := filepath.Clean(filepath.Join(filepath.Dir(resolved), "..", "share", "omnipus"))
	require.NoError(t, os.MkdirAll(own, 0o755))
	t.Cleanup(func() { _ = os.RemoveAll(own) })
	return own
}

// functionalCopyTree copies a real provisioned program tree with `cp -a`.
// Test-only fixture preparation; this never runs inside the converter and
// never changes the source install. GNU cp preserves links; `--reflink=auto`
// is GNU-only, so Linux keeps it and every other GOOS copies without it
// (plain -a preserves symlinks on darwin — D17 moved this helper out of the
// Linux-only recorder file because the turn-repeat row, darwin || linux,
// stages its own-folder registrations through it on both GOOS).
func functionalCopyTree(t *testing.T, source, target string) {
	t.Helper()
	args := []string{"-a", "--", source, target}
	if runtime.GOOS == "linux" {
		args = []string{"-a", "--reflink=auto", "--", source, target}
	}
	cmd := exec.Command("cp", args...)
	output, err := cmd.CombinedOutput()
	require.NoError(t, err, "copy the real Office fixture: %s", output)
}
