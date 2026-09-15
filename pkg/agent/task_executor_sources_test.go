package agent

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// readTaskExecutorSourcesForTest returns the concatenated non-test sources of
// the task executor: every task_executor*.go file in pkg/agent/, in name
// order, each preceded by a "// ---- file: <name> ----" banner. Guard tests
// that used to read "task_executor.go" alone must read this instead:
// task_executor.go was split into task_executor_run.go /
// task_executor_judge.go by job on 2026-09-15, so a symbol scanned by name
// may live in any of them.
func readTaskExecutorSourcesForTest(t *testing.T) string {
	t.Helper()
	matches, err := filepath.Glob("task_executor*.go")
	require.NoError(t, err, "readTaskExecutorSourcesForTest: glob")
	var b strings.Builder
	n := 0
	for _, name := range matches {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		b.WriteString("// ---- file: " + name + " ----\n")
		b.WriteString(readOwnedFileForTest(t, name))
		b.WriteString("\n")
		n++
	}
	require.Greater(t, n, 0, "readTaskExecutorSourcesForTest: no task_executor*.go sources found")
	return b.String()
}
