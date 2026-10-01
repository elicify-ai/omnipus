package tools

// RED (combined feature-gate review, item 6, security-lead — LOW).
// Oracle: the finding itself — resolveMailAttachments should check a
// candidate attachment's file SIZE before reading its contents, not after,
// so an oversized file is never fully materialized in memory just to be
// rejected. Read confirms the current code does the opposite:
// pkg/tools/email_compose.go::resolveMailAttachments calls
// handle.ReadFile() (pkg/tools/resolvepath.go::PathHandle.ReadFile, which is
// a bare os.ReadFile with no size pre-check) and only AFTER that read
// checks `total+len(data) > maxMailAttachmentBytes` (MC-32, 25 MiB). There
// is no os.Stat/handle.Stat call anywhere on this path.
//
// This proves the read-before-check behavior via a memory-allocation
// signal (the brief's own suggested proof: "via a size-tracking reader or
// by timing/memory evidence") rather than by modifying production code: a
// stat-first implementation would reject a grossly oversized file from its
// size alone, allocating close to nothing; the current implementation
// allocates the whole file's bytes before it ever compares against the cap.

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveMailAttachments_ReadsWholeOversizedFileBeforeRejecting(t *testing.T) {
	ws := t.TempDir()
	path := filepath.Join(ws, "huge.bin")
	const oversizeBytes = 64 << 20 // 64 MiB — well over the 25 MiB cap (MC-32), comfortably distinguishable from stat-only allocation noise

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(oversizeBytes); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	ctx := WithTurnWorkspaceDir(mailCtx(), ws)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	_, attErr := resolveMailAttachments(ctx, "send_email", []any{"huge.bin"})

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	if attErr == nil {
		t.Fatal("MC-32: a 64 MiB attachment must be rejected (the cap is 25 MiB total)")
	}
	if !strings.Contains(attErr.Error(), "25 MiB") {
		t.Fatalf("MC-32: error %q does not name the 25 MiB cap", attErr)
	}

	allocated := after.TotalAlloc - before.TotalAlloc
	const halfFile = oversizeBytes / 2
	if allocated >= halfFile {
		t.Fatalf("resolveMailAttachments allocated %d bytes handling a %d-byte oversized attachment before "+
			"rejecting it (want well under %d) — the size check runs AFTER reading the whole file into memory "+
			"(handle.ReadFile, i.e. os.ReadFile, has no size pre-check); a stat-first implementation would reject "+
			"from the file's size alone without ever materializing its bytes",
			allocated, oversizeBytes, halfFile)
	}
}
