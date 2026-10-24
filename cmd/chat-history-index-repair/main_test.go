package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/elicify-ai/omnipus/pkg/session"
	"github.com/stretchr/testify/require"
)

// D13/T22/T25/T30: build/invoke the real separate command, never a fake repair
// engine or a product/gateway constructor. Missing production is a named fatal,
// not a compile accident, missing executable interpreted as a refusal, or skip.
func repairBinary(t *testing.T) string {
	t.Helper()
	if _, err := os.Stat("main.go"); errors.Is(err, fs.ErrNotExist) {
		t.Fatal("BLOCKED: standalone Go command main.go not implemented — required by D13/T22/T25/T30/B44; command-package discovery alone does not prove a command was built or ran")
	} else {
		require.NoError(t, err)
	}
	name := "chat-history-index-repair"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	out := filepath.Join(t.TempDir(), name)
	cmd := exec.Command("go", "build", "-tags", "goolm,stdjson", "-o", out, ".")
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
	b, err := cmd.CombinedOutput()
	require.NoError(t, err, "standalone command build, exit/output: %s", b)
	t.Log("standalone build exit=0 (CGO_ENABLED=0; tags goolm,stdjson)")
	return out
}

func repairRun(t *testing.T, binary string, wantExit int, args ...string) string {
	t.Helper()
	cmd := exec.Command(binary, args...)
	b, err := cmd.CombinedOutput()
	exit := 0
	if err != nil {
		var ee *exec.ExitError
		require.ErrorAs(t, err, &ee, "real command invocation, not a missing/undispatchable executable")
		exit = ee.ExitCode()
	}
	t.Logf("command phase=%s exit=%d output=%s", args[0], exit, b)
	require.Equal(t, wantExit, exit, "D13 exact refusal/success exit; output=%s", b)
	return string(b)
}

type repairFileFact struct { // not-wire-format: independent test-only filesystem equality oracle.
	Mode   fs.FileMode
	Size   int64
	Mtime  int64
	Digest string
}

func repairTree(t *testing.T, root string) map[string]repairFileFact {
	t.Helper()
	out := map[string]repairFileFact{}
	require.NoError(t, filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fact := repairFileFact{Mode: info.Mode(), Size: info.Size(), Mtime: info.ModTime().UnixNano()}
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			sum := sha256.Sum256(b)
			fact.Digest = hex.EncodeToString(sum[:])
		}
		out[filepath.ToSlash(rel)] = fact
		return nil
	}))
	return out
}

func repairCopy(t *testing.T, src, dst string) {
	t.Helper()
	var dirs []string
	require.NoError(t, filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		info, err := d.Info()
		if err != nil {
			return err
		}
		if d.IsDir() {
			dirs = append(dirs, path)
			return os.MkdirAll(target, info.Mode().Perm())
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("fixture refuses unsupported kind %s", path)
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err = os.WriteFile(target, b, info.Mode().Perm()); err != nil {
			return err
		}
		return os.Chtimes(target, info.ModTime(), info.ModTime())
	}))
	for i := len(dirs) - 1; i >= 0; i-- {
		info, err := os.Stat(dirs[i])
		require.NoError(t, err)
		rel, err := filepath.Rel(src, dirs[i])
		require.NoError(t, err)
		require.NoError(t, os.Chtimes(filepath.Join(dst, rel), info.ModTime(), info.ModTime()))
	}
}

func repairCanonicalFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	realRoot, err := filepath.EvalSymlinks(root)
	require.NoError(t, err)
	require.NoError(t, os.MkdirAll(filepath.Join(realRoot, "uploads", "empty"), 0o700))
	for name, body := range map[string]string{"config.json": "{\"version\":1}", "credentials.json": "encrypted-backup-canary", "uploads/zero.bin": "", "uploads/évidence.txt": "frozen backup bytes"} {
		require.NoError(t, os.WriteFile(filepath.Join(realRoot, filepath.FromSlash(name)), []byte(body), 0o600))
	}
	store, err := session.NewUnifiedStore(filepath.Join(realRoot, "sessions"))
	require.NoError(t, err)
	m, err := store.NewSession(session.SessionTypeChat, "webchat", "mia")
	require.NoError(t, err)
	for i := 1; i <= 40; i++ {
		require.NoError(t, store.AppendTranscriptStrict(m.ID, session.TranscriptEntry{ID: fmt.Sprintf("entry-%03d", i), Role: "user", Content: "canonical fixture", AgentID: "mia", Timestamp: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}))
	}
	require.NoError(t, store.Close(), "finish background stats writer before independent tree checks")
	return realRoot
}

func repairManifest(t *testing.T, binary, source, backup string) string {
	t.Helper()
	repairCopy(t, source, backup)
	manifest := filepath.Join(t.TempDir(), "backup-manifest.json")
	beforeSource, beforeBackup := repairTree(t, source), repairTree(t, backup)
	repairRun(t, binary, 0, "manifest", "--data-root", source, "--backup-root", backup, "--output", manifest)
	require.Equal(t, beforeSource, repairTree(t, source), "manifest producer writes no source bytes/modes/mtime")
	require.Equal(t, beforeBackup, repairTree(t, backup), "manifest producer does not change verified copy")
	return manifest
}

func TestHistoryRepairCommand_PreflightManifestAndIdempotence(t *testing.T) {
	binary := repairBinary(t)
	t.Run("initial_dry_run_needs_no_backup_or_stop", func(t *testing.T) {
		source := repairCanonicalFixture(t)
		before := repairTree(t, source)
		first := repairRun(t, binary, 0, "dry-run", "--data-root", source)
		second := repairRun(t, binary, 0, "dry-run", "--data-root", source)
		require.Equal(t, first, second, "deterministic stdout-only plan on unchanged closed source")
		require.Contains(t, first, "mode=dry-run")
		require.Contains(t, first, "groups=40")
		require.Contains(t, first, "canonical_writes=0")
		require.Contains(t, first, "refused=0")
		require.Equal(t, before, repairTree(t, source), "independent actual tree: no source/index/temp/lock/log write/chmod/mtime change")
	})
	t.Run("manifest_inventories_actual_full_copy", func(t *testing.T) {
		source := repairCanonicalFixture(t)
		backup := t.TempDir()
		backup, err := filepath.EvalSymlinks(backup)
		require.NoError(t, err)
		manifest := repairManifest(t, binary, source, backup)
		b, err := os.ReadFile(manifest)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		require.EqualValues(t, 1, m["schema_version"])
		require.Equal(t, source, m["source_root"])
		require.Equal(t, backup, m["backup_root"])
		require.Regexp(t, `^[0-9a-f]{64}$`, m["canonical_fingerprint"])
		entries, ok := m["entries"].([]any)
		require.True(t, ok, "entries is non-null full inventory")
		paths := make([]string, 0, len(entries))
		byPath := map[string]map[string]any{}
		for _, raw := range entries {
			e, ok := raw.(map[string]any)
			require.True(t, ok)
			path, ok := e["path"].(string)
			require.True(t, ok)
			paths = append(paths, path)
			byPath[path] = e
		}
		wantPaths := make([]string, 0)
		facts := repairTree(t, source)
		for path := range facts {
			if path != "." {
				wantPaths = append(wantPaths, path)
			}
		}
		sort.Strings(wantPaths)
		require.Equal(t, wantPaths, paths, "no sessions-only or partial-backup masquerade; sorted normalized UTF-8 paths")
		for _, path := range []string{"config.json", "credentials.json", "uploads/zero.bin", "uploads/évidence.txt"} {
			e := byPath[path]
			require.Equal(t, "file", e["kind"])
			require.EqualValues(t, facts[path].Size, e["size"])
			require.Equal(t, facts[path].Digest, e["sha256"])
			require.Equal(t, fmt.Sprint(facts[path].Mtime), e["mtime_unix_ns"], "string retains native nanosecond precision")
		}
		require.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", byPath["uploads/zero.bin"]["sha256"], "frozen SHA-256 empty-file vector")
	})
	t.Run("repeat_and_interruption_require_real_publication_seam", func(t *testing.T) {
		repairSharedCoreRoundTrip(t, repairCanonicalFixture(t))
	})
}

func TestHistoryRepairCommand_SharedConstructorAndRealReader(t *testing.T) {
	binary := repairBinary(t)
	source := repairCanonicalFixture(t)
	before := repairTree(t, source)
	repairRun(t, binary, 0, "dry-run", "--data-root", source)
	require.Equal(t, before, repairTree(t, source), "initial real command preflight is read-only")
	repairSharedCoreRoundTrip(t, source)
}

func TestHistoryRepairCommand_BuiltPhasesCutoverAndManifest(t *testing.T) {
	binary := repairBinary(t)
	t.Run("empty_tree_manifest_has_real_empty_fingerprint", func(t *testing.T) {
		source := t.TempDir()
		source, err := filepath.EvalSymlinks(source)
		require.NoError(t, err)
		backup := t.TempDir()
		backup, err = filepath.EvalSymlinks(backup)
		require.NoError(t, err)
		manifest := repairManifest(t, binary, source, backup)
		b, err := os.ReadFile(manifest)
		require.NoError(t, err)
		var m map[string]any
		require.NoError(t, json.Unmarshal(b, &m))
		require.Equal(t, []any{}, m["entries"], "empty is not null")
		require.Equal(t, "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855", m["canonical_fingerprint"], "D13 empty tuple stream frozen vector")
	})
	t.Run("backup_extra_missing_tamper_refuse", func(t *testing.T) {
		for _, fault := range []string{"extra", "missing", "tamper"} {
			t.Run(fault, func(t *testing.T) {
				source := repairCanonicalFixture(t)
				backup := t.TempDir()
				backup, err := filepath.EvalSymlinks(backup)
				require.NoError(t, err)
				manifest := repairManifest(t, binary, source, backup)
				switch fault {
				case "extra":
					require.NoError(t, os.WriteFile(filepath.Join(backup, "unlisted.bin"), []byte("extra"), 0o600))
				case "missing":
					require.NoError(t, os.Remove(filepath.Join(backup, "credentials.json")))
				case "tamper":
					require.NoError(t, os.WriteFile(filepath.Join(backup, "config.json"), []byte("tampered"), 0o600))
				}
				beforeSource, beforeBackup := repairTree(t, source), repairTree(t, backup)
				out := repairRun(t, binary, 2, "verify", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest)
				require.Contains(t, out, "backup_", "fixed refusal class, never success boolean")
				require.Equal(t, beforeSource, repairTree(t, source))
				require.Equal(t, beforeBackup, repairTree(t, backup))
			})
		}
	})
	t.Run("unconverted_first_before_backup_checks", func(t *testing.T) {
		source := repairCanonicalFixture(t)
		legacy := filepath.Join(source, "sessions", ".context")
		require.NoError(t, os.MkdirAll(legacy, 0o700))
		require.NoError(t, os.WriteFile(filepath.Join(legacy, "legacy.jsonl"), []byte("{\"role\":\"user\",\"content\":\"old bytes\",\"ts\":1}\n"), 0o600))
		require.NoError(t, os.WriteFile(filepath.Join(legacy, "legacy.meta.json"), []byte(`{"key":"legacy","skip":0,"count":1}`), 0o600))
		before := repairTree(t, source)
		out := repairRun(t, binary, 2, "dry-run", "--data-root", source)
		require.Contains(t, out, "unconverted")
		require.Equal(t, before, repairTree(t, source), "repair must never run existing cutover itself")
	})
	t.Run("missing_stop_evidence_refuses_before_publication", func(t *testing.T) {
		source := repairCanonicalFixture(t)
		backup := t.TempDir()
		backup, err := filepath.EvalSymlinks(backup)
		require.NoError(t, err)
		manifest := repairManifest(t, binary, source, backup)
		before := repairTree(t, source)
		out := repairRun(t, binary, 2, "apply", "--data-root", source, "--backup-root", backup, "--backup-manifest", manifest, "--confirm-stopped")
		require.Contains(t, out, "stopped_evidence_missing")
		require.Equal(t, before, repairTree(t, source), "all-session preflight before any generation")
	})
	t.Run("schema_cutover_and_exact_aggregate_exit_matrix", func(t *testing.T) {
		repairRefusalMatrix(t, binary)
	})
}
