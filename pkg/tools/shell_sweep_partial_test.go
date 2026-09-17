package tools

// Regression coverage for the Claude review 2026-09-14 cut-list finding on
// pkg/tools/shell.go's post-command sweep: every foreground bash walks up to
// 20k entries / 400ms, and when the budget dies inside the workspace every
// result carried a "workspace too large" banner while the MOUNTS were then
// never inspected at all — a banner a reader could mistake for "the mounts
// were checked". These tests pin the honest accounting: the sweep result
// names what was swept, what was only partly swept and what was skipped, the
// notice says so in those words, and sweepAfterRun logs one WARN per run
// listing the skipped roots.
//
// New, uniquely-named file (no existing test file is modified) because the
// shared shell_escape_sweep_test.go is owned by other fix clusters.
//
// Run: CGO_ENABLED=0 go test -tags goolm,stdjson -run 'PartialSweep|SweepAfterRun' -p 1 ./pkg/tools/

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shrinkSweepCaps deterministically forces a partial sweep by shrinking the
// package-level caps, restoring them when the test ends.
func shrinkSweepCaps(t *testing.T, maxEntries int) {
	t.Helper()
	oldEntries := escapeSweepMaxEntries
	escapeSweepMaxEntries = maxEntries
	t.Cleanup(func() { escapeSweepMaxEntries = oldEntries })
}

// TestSweepEscapingSymlinks_PartialSweepNamesWhatWasSkipped: with the entry
// cap set below the workspace's entry count, the walk dies inside the
// workspace root and the mounts are never reached — the exact production
// shape (roots are [workspace, mount1, mount2, …] in order). The result must
// record that, and the notice must say it.
func TestSweepEscapingSymlinks_PartialSweepNamesWhatWasSkipped(t *testing.T) {
	ws := sweepTempDir(t)
	mountA := sweepTempDir(t)
	mountB := sweepTempDir(t)

	// More entries in the workspace than the shrunk cap allows.
	for i := 0; i < 5; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(ws, "f"+string(rune('a'+i))+".txt"), []byte("x"), 0o644))
	}
	// An escaping link INSIDE a mount the sweep will never reach — the skip
	// is real, not just unreported bookkeeping.
	require.NoError(t, os.Symlink("/etc", filepath.Join(mountA, "unseen-escape")))

	shrinkSweepCaps(t, 3)
	res := sweepEscapingSymlinks([]string{ws, mountA, mountB}, []string{ws, mountA, mountB}, time.Now())

	require.True(t, res.Partial, "the entry cap must stop the walk")
	assert.Equal(t, ws, res.PartialRoot, "the budget died inside the workspace root")
	assert.Empty(t, res.Swept, "no root was fully inspected")
	assert.Equal(t, []string{mountA, mountB}, res.Skipped, "both mounts were never inspected")
	assert.Empty(t, res.Found, "the mount's escaping link is in a skipped root — the partial sweep cannot see it")

	notice := escapeSweepNotice(res)
	assert.Contains(t, notice, "PARTIAL", "the notice must say the check was partial")
	assert.Contains(t, notice, ws, "the notice must name where the budget died")
	assert.Contains(t, notice, mountA, "the notice must name the first skipped mount")
	assert.Contains(t, notice, mountB, "the notice must name the second skipped mount")
	assert.Contains(t, notice, "NOT inspected at all", "the notice must say the skipped roots were not looked at")
}

// TestEscapeSweepNotice_PartialWithSweptRootsAlsoNamesThem: when SOME roots
// completed before the budget died, the notice says which.
func TestEscapeSweepNotice_PartialWithSweptRootsAlsoNamesThem(t *testing.T) {
	notice := escapeSweepNotice(escapeSweepResult{
		Partial:     true,
		Swept:       []string{"/ws"},
		PartialRoot: "/mountA",
		Skipped:     []string{"/mountB"},
	})
	assert.Contains(t, notice, "Fully inspected: /ws.")
	assert.Contains(t, notice, "budget exhausted while walking /mountA")
	assert.Contains(t, notice, "NOT inspected at all: /mountB")
}

// TestEscapeSweepNotice_CompleteSweepStaysSilent pins the unchanged
// contract: a complete sweep with no findings renders nothing.
func TestEscapeSweepNotice_CompleteSweepStaysSilent(t *testing.T) {
	assert.Empty(t, escapeSweepNotice(escapeSweepResult{Swept: []string{"/ws"}}))
}

// TestSweepAfterRun_WarnsOncePerPartialRun: sweepAfterRun must log ONE WARN
// naming the skipped roots when the sweep is partial — the operator-facing
// half of the honesty fix (the notice lives in the tool result an agent can
// paraphrase away; the log is what a human reviews). Asserted on the rendered
// text-handler output of a swapped-in default logger.
func TestSweepAfterRun_WarnsOncePerPartialRun(t *testing.T) {
	ws := sweepTempDir(t)
	for i := 0; i < 5; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(ws, "f"+string(rune('a'+i))+".txt"), []byte("x"), 0o644))
	}
	tool, err := NewExecTool(ws, true)
	require.NoError(t, err)

	var buf strings.Builder
	orig := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&nopFlushWriter{&buf}, nil)))
	t.Cleanup(func() { slog.SetDefault(orig) })

	shrinkSweepCaps(t, 3)
	result := tool.Execute(context.Background(), map[string]any{"action": "run", "command": "true"})
	require.NotNil(t, result)

	logged := buf.String()
	assert.Equal(t, 1, strings.Count(logged, "sweep was partial"),
		"exactly one WARN per run must reach the gateway log")
	assert.Contains(t, logged, "skipped", "the WARN must carry the skipped roots")

	// The tool result itself must also carry the honest partial banner. This
	// run has no mounts, so Skipped is empty and the banner's honest wording
	// is "every other root was fully inspected" — the skipped-roots wording
	// is pinned by TestSweepEscapingSymlinks_PartialSweepNamesWhatWasSkipped.
	assert.Contains(t, result.ForLLM, "PARTIAL")
	assert.Contains(t, result.ForLLM, "Every other root was fully inspected.")
	assert.NotContains(t, result.ForLLM, "workspace too large to inspect",
		"the old dishonest banner must be gone")
}

type nopFlushWriter struct{ b *strings.Builder }

func (w *nopFlushWriter) Write(p []byte) (int, error) { return w.b.Write(p) }

// TestEscapeSweepMountsUnresolvedNote pins the honesty note appended when
// findings are reported but the turn's mount list could not be resolved
// (ResolveTurnFSPolicy error — the PLAUSIBLE half of the finding): the sweep
// is report-only, so the worst case is an over-broad finding, and the note
// says so instead of letting "outside the workspace and its mounts" stand
// over mounts it never enumerated.
func TestEscapeSweepMountsUnresolvedNote(t *testing.T) {
	note := escapeSweepMountsUnresolvedNote()
	assert.Contains(t, note, "mount list could not be resolved")
	assert.Contains(t, note, "MOUNTED folder")
	assert.True(t, strings.HasPrefix(note, "\n"), "the note appends to an existing notice line")
}
