package tools

// Regression coverage for silent-failure review 2026-09-14 F9 (fix4 H1): the
// background bash completion goroutine runs the report-only symlink sweep
// after the turn has ended, with nothing above it to catch a panic. A panic
// there used to take the whole gateway process down, recorded only on
// stderr. The goroutine must instead survive, still deliver the completion
// through the callback with a plain notice that the check did not run, log
// the panic at ERROR with its stack, and write an audit warning the operator
// can find.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run '^TestExecTool_BackgroundSweepPanic' -p 1 ./pkg/tools/

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/elicify-ai/omnipus/pkg/audit"
)

// lockedLogBuffer is a goroutine-safe sink for a swapped-in slog handler: the
// completion goroutine writes to it while the test later reads it.
type lockedLogBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (w *lockedLogBuffer) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.b.Write(p)
	if err != nil {
		return 0, fmt.Errorf("write: %w", err)
	}
	return n, nil
}

func (w *lockedLogBuffer) String() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.b.String()
}

// TestExecTool_BackgroundSweepPanicIsRecovered injects a panic into the
// background completion sweep through the backgroundSweepFn seam. Without a
// recover in that path the panic escapes a bare goroutine and kills the test
// binary — the same way it would kill the gateway.
func TestExecTool_BackgroundSweepPanicIsRecovered(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("test uses a POSIX shell command")
	}
	ws := sweepTempDir(t)
	tool, err := NewExecTool(ws, true)
	require.NoError(t, err)
	tool.godMode = true // no kernel sandbox needed to run `echo`; the seam below replaces the sweep anyway

	auditDir := t.TempDir()
	auditLog, err := audit.NewLogger(audit.LoggerConfig{Dir: auditDir, RetentionDays: 1})
	require.NoError(t, err)
	defer func() { _ = auditLog.Close() }()
	tool.SetAuditLogger(auditLog)

	logs := &lockedLogBuffer{}
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(logs, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })

	const panicMsg = "injected sweep failure for fix4 H1"
	sweepCalled := make(chan struct{}, 1)
	tool.backgroundSweepFn = func(context.Context, string, string, string, time.Time, *ToolResult) *ToolResult {
		sweepCalled <- struct{}{}
		panic(panicMsg)
	}

	ctx := WithToolContext(context.Background(), "cli", "")
	done := make(chan *ToolResult, 1)
	start := tool.ExecuteAsync(ctx, map[string]any{
		"action":            "run",
		"command":           "echo BG_SWEEP_PANIC_RUN",
		"run_in_background": true,
		"timeout_seconds":   float64(30),
	}, func(_ context.Context, result *ToolResult) { done <- result })
	require.NotNil(t, start)
	require.False(t, start.IsError, "background start must succeed, got ForLLM=%q", start.ForLLM)

	var completion *ToolResult
	select {
	case completion = <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("background completion callback never fired after the sweep panicked")
	}
	require.NotNil(t, completion)

	select {
	case <-sweepCalled:
	default:
		t.Fatal("the injected sweep was never called — the test did not exercise the panic path")
	}

	// The callback still carries the real session outcome...
	assert.Contains(t, completion.ForLLM, "BG_SWEEP_PANIC_RUN", "the command's output must still be delivered")
	assert.Contains(t, completion.ForLLM, "finished", "the completion must still report the session outcome")
	// ...plus a plain statement that the post-run check did not happen.
	assert.Contains(t, completion.ForLLM, "post-command symlink check FAILED",
		"the delivered result must say the post-run sweep failed")
	assert.Contains(t, completion.ForLLM, "NOT checked",
		"the notice must not let a reader assume the run was checked")
	assert.NotContains(t, completion.ForLLM, panicMsg,
		"the internal panic text belongs in the operator log, not in the agent-facing result")

	// The operator log: one ERROR carrying the panic value and a stack.
	logged := logs.String()
	assert.Equal(t, 1, strings.Count(logged, "symlink sweep panicked"), "exactly one ERROR per failed sweep; log was:\n%s", logged)
	assert.Contains(t, logged, "level=ERROR")
	assert.Contains(t, logged, panicMsg, "the ERROR must carry the panic value")
	assert.Contains(t, logged, "goroutine ", "the ERROR must carry the stack")

	// The audit trail: a warning entry an operator reviewing audit finds.
	require.NoError(t, auditLog.Close())
	files, err := filepath.Glob(filepath.Join(auditDir, "*.jsonl"))
	require.NoError(t, err)
	// bytes.Buffer, not a preallocated slice: the total size depends on the
	// audit logger's rotation, not on anything this test controls, so there is
	// no honest capacity to pass to make(). Buffer grows amortized and gives
	// prealloc no `var x []T` + append-in-range shape to flag — the cause is
	// gone rather than the report silenced.
	var all bytes.Buffer
	for _, f := range files {
		b, rerr := os.ReadFile(f)
		require.NoError(t, rerr)
		all.Write(b)
	}
	assert.Contains(t, all.String(), `"warning":"escaping_symlink_sweep_failed"`)
	assert.Contains(t, all.String(), panicMsg)
	assert.Contains(t, all.String(), "echo BG_SWEEP_PANIC_RUN", "the audit entry must name the command whose run went unchecked")
}
