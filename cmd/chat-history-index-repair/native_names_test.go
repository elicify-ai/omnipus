package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// Live-install shapes (expected values from the fix brief R9-R11, not from the
// implementation): the product creates task sessions named
// agent:<agent>:task:<task> (pkg/agent task_executor_run.go), some of which
// have no meta.json; the product's own session list skips a directory whose
// meta.json is missing ("skipping unreadable out-of-band session dir").

const (
	repairTaskAgentUUID = "0f6d3c52-8f8e-4c55-9c39-5d3c1b1a2e01"
	repairTaskUUID      = "7b2e9a10-4d6f-4f0e-a7a4-2c9d8e1f3b02"
	repairTaskUUID2     = "c1d2e3f4-a5b6-4c7d-8e9f-0a1b2c3d4e03"
)

// repairTaskSessionName returns the product's task-session directory name. A
// ':' cannot be part of a Windows file name, so Windows uses '-' instead; the
// skip rule under test does not depend on the separator.
func repairTaskSessionName(task string) string {
	sep := ":"
	if runtime.GOOS == "windows" {
		sep = "-"
	}
	return "agent" + sep + repairTaskAgentUUID + sep + "task" + sep + task
}

// repairGuardedApply runs the full guarded CLI procedure on root: capture the
// running native instance, stop it, take a link-preserving full backup and
// manifest, produce the stopped receipt and apply once.
func repairGuardedApply(t *testing.T, tool, helper, root string) (string, string, string) {
	t.Helper()
	child := repairStartNative(t, helper, root)
	binding, identity := repairCapture(t, tool, helper, root, child)
	repairStopNative(t, child, "stop", 0)
	backup, manifest := repairLinkManifest(t, tool, root)
	receipt := repairProduceReceipt(t, tool, root, binding, identity, "absent")
	out := repairRun(t, tool, 0, "apply", "--data-root", root, "--backup-root", backup, "--backup-manifest", manifest, "--instance-binding", binding, "--stopped-receipt", receipt, "--confirm-stopped")
	return out, backup, manifest
}

// repairTaskSessionsFixture adds, next to the fixture's real session, a real
// product task session (meta.json present, valid product session ID) and an
// out-of-band task directory without meta.json that holds transcript bytes.
func repairTaskSessionsFixture(t *testing.T, root string) (realTask, orphan string) {
	t.Helper()
	realTask, orphan = repairTaskSessionName(repairTaskUUID), repairTaskSessionName(repairTaskUUID2)
	store, err := session.NewUnifiedStore(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	_, err = store.GetOrCreateScheduledSession(realTask, "mia")
	require.NoError(t, err, "the product accepts the task-session name as a session ID")
	for _, id := range []string{"task-001", "task-002"} {
		require.NoError(t, store.AppendTranscriptStrict(realTask, session.TranscriptEntry{ID: id, Role: "user", Content: "task fixture", AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 13, 0, 0, 0, time.UTC)}))
	}
	require.NoError(t, store.Close())
	require.NoError(t, os.RemoveAll(filepath.Join(root, "sessions", realTask, session.ChatHistoryIndexDirectory)))
	dir := filepath.Join(root, "sessions", orphan)
	require.NoError(t, os.Mkdir(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "transcript.jsonl"), []byte("{\"id\":\"orphan\"}\n"), 0o600))
	_, err = os.Stat(filepath.Join(dir, "meta.json"))
	require.ErrorIs(t, err, os.ErrNotExist, "instrument: the out-of-band directory has no meta.json")
	return realTask, orphan
}

// R11b / R10: a task directory without meta.json is skipped like the product
// skips it — never refused, never indexed, counted — while it stays in the
// backup equality; a real task session alongside it is published.
func TestHistoryRepairCommand_MetaLessTaskDirSkippedAndCounted(t *testing.T) {
	tool, helper := repairBinary(t), repairNativeBinary(t)
	root, sid := repairMissingIndexFixture(t)
	realTask, orphan := repairTaskSessionsFixture(t, root)
	orphanDir := filepath.Join(root, "sessions", orphan)
	orphanBefore := repairTree(t, orphanDir)

	before := repairTree(t, root)
	out := repairRun(t, tool, 0, "dry-run", "--data-root", root)
	require.Contains(t, out, "mode=dry-run sessions=2 create=2 ")
	require.Contains(t, out, " refused=0 skipped=1 ")
	require.Contains(t, out, "session=\""+orphan+"\" action=skipped reason=meta_missing")
	require.Contains(t, out, "session="+realTask+" action=create")
	require.Equal(t, before, repairTree(t, root), "dry-run is read-only")

	out, backup, manifest := repairGuardedApply(t, tool, helper, root)
	repairAssertPublicationCounts(t, out, 2, 0, 0)
	require.Contains(t, out, " skipped=1 ")
	for _, id := range []string{sid, realTask} {
		require.NoError(t, session.VerifyChatHistoryIndex(t.Context(), filepath.Join(root, "sessions", id)), "real session %s published", id)
	}
	_, err := os.Stat(filepath.Join(orphanDir, session.ChatHistoryIndexDirectory))
	require.ErrorIs(t, err, os.ErrNotExist, "the skipped directory is never indexed")
	require.Equal(t, orphanBefore, repairTree(t, orphanDir), "the skipped directory is untouched")
	_, byPath := repairManifestEntries(t, manifest)
	e, ok := byPath["sessions/"+orphan+"/transcript.jsonl"]
	require.True(t, ok, "the skipped directory is canonical data the backup must hold")
	require.Equal(t, "file", e["kind"])
	out = repairRun(t, tool, 0, "verify", "--data-root", root, "--backup-root", backup, "--backup-manifest", manifest)
	require.Contains(t, out, " skipped=1 ")
}

// R11b: a backup missing the skipped directory is incomplete.
func TestHistoryRepairCommand_MetaLessTaskDirRequiredInBackup(t *testing.T) {
	tool := repairBinary(t)
	root, _ := repairMissingIndexFixture(t)
	_, orphan := repairTaskSessionsFixture(t, root)
	backup, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	repairCopyPreservingLinks(t, root, backup)
	require.NoError(t, os.RemoveAll(filepath.Join(backup, "sessions", orphan)))
	sessionsInfo, err := os.Stat(filepath.Join(root, "sessions"))
	require.NoError(t, err)
	require.NoError(t, os.Chtimes(filepath.Join(backup, "sessions"), sessionsInfo.ModTime(), sessionsInfo.ModTime()))
	manifest := filepath.Join(t.TempDir(), "backup-manifest.json")
	out := repairRun(t, tool, 2, "manifest", "--data-root", root, "--backup-root", backup, "--output", manifest)
	require.Contains(t, out, "mode=manifest class=backup_incomplete")
	repairNoEvidence(t, manifest)
}

// R11c / R9: NUL, '..' parts and case-insensitive duplicates still refuse.
// They cannot all exist on disk, so they are planted in a valid manifest.
func TestHistoryRepairCommand_ManifestUnsafeNamesStillRefused(t *testing.T) {
	tool := repairBinary(t)
	for name, path := range map[string]string{
		"nul":            "credentials.json\x00",
		"dotdot":         "uploads/../config.json",
		"case_duplicate": "uploads/ZERO.bin",
	} {
		t.Run(name, func(t *testing.T) {
			source := repairCanonicalFixture(t)
			backup, manifest := repairLinkManifest(t, tool, source)
			m, byPath := repairManifestEntries(t, manifest)
			planted := map[string]any{}
			for k, v := range byPath["uploads/zero.bin"] {
				planted[k] = v
			}
			planted["path"] = path
			entries := append(m["entries"].([]any), planted)
			sort.Slice(entries, func(i, j int) bool {
				return entries[i].(map[string]any)["path"].(string) < entries[j].(map[string]any)["path"].(string)
			})
			m["entries"] = entries
			b, err := json.Marshal(m)
			require.NoError(t, err)
			require.True(t, strings.Contains(string(b), `"path":"`+strings.ReplaceAll(path, "\x00", `\u0000`)+`"`), "instrument: the planted name is in the manifest")
			require.NoError(t, os.WriteFile(manifest, b, 0o600))
			out := repairRun(t, tool, 2, "verify", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest)
			require.Contains(t, out, "class=unsafe_path")
		})
	}
}
