package agent

// Round 6, CK-1, dispatch level. r17 3.5/3.6 (rows 9.9, 9.11): when the saved
// program is entered through a DIRECTORY symlink, the driver still receives
// the fully resolved checked file, so a later retarget of the directory link
// cannot change what runs.

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpawn_ResolveCheckExec_DirectoryLink_DriverGetsResolvedPath_9_11(t *testing.T) {
	f := t8RunFixture(t)
	real := l11274Executable(t, f.home, "codex", true)
	dirLink := filepath.Join(t.TempDir(), "tools")
	t8Symlink(t, dirLink, filepath.Dir(real))
	entered := filepath.Join(dirLink, filepath.Base(real))
	f.setProgram(t, "codex", entered)
	id, _ := t8CreateCodexWorker(t, f)

	opts := t8RunOptions(t, f, id)
	want, err := filepath.EvalSymlinks(real)
	require.NoError(t, err)
	assert.NotEqual(t, entered, opts.CLIPath, "the entered path through the directory link is not what runs")
	assert.Equal(t, want, opts.CLIPath, "the driver receives the fully resolved checked file")
	if _, statErr := os.Stat(opts.CLIPath); statErr != nil {
		t.Fatalf("the resolved path must exist: %v", statErr)
	}
}
