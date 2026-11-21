package preview

import (
	"os"
	"path/filepath"
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
