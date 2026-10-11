package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// S1 final-gate M14: the cancel/truncation docs must not cite the removed
// MarkLastEntryTruncated as a live mechanism (or tell a maintainer to keep a
// list in step with session.validTruncationReasons, which no longer exists).
func TestCancelDocsDoNotCiteRemovedTruncationSymbols(t *testing.T) {
	// Control: the file scan sees real production files, and sees an identifier
	// that is known to be present.
	cancelSrc, err := os.ReadFile("cancel.go")
	require.NoError(t, err)
	require.Contains(t, string(cancelSrc), "RequestCancel", "control: cancel.go was read")

	files, err := filepath.Glob("*.go")
	require.NoError(t, err)
	checked := 0
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		data, rerr := os.ReadFile(f)
		require.NoError(t, rerr)
		checked++
		for _, banned := range []string{"MarkLastEntryTruncated", "session.validTruncationReasons"} {
			require.NotContains(t, string(data), banned, "%s still cites the removed %s", f, banned)
		}
	}
	require.Greater(t, checked, 50, "control: the scan covered the package's production files")
}
